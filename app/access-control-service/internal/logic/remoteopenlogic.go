package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// commandOpenDoor 远程开门命令, M1 侧约定的命令名.
	commandOpenDoor = "open_door"
	// m1CommandTimeout M1 下发指令的短超时: 设备侧可能在离线/弱网下长时间无响应,
	// 不能让 HTTP 线程被拖住, 超时按独立结果记入审计(docs/m3/04 #47 的 M3-E-2006).
	m1CommandTimeout = 800 * time.Millisecond
)

// RemoteOpenLogic 远程开门逻辑: gRPC 调 M1 SendCommand 并落开门审计(#47).
// 成功/失败/超时三种结果都必须写 access_operate_log, 供事后追责.
type RemoteOpenLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRemoteOpenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RemoteOpenLogic {
	return &RemoteOpenLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// RemoteOpen 下发开门命令.
// 返回给调用方的成功只包含"命令已下发且设备确认"的情况; M1 失败/超时均返回错误码 M3-E-2003,
// 但审计表里仍会留下一条失败/超时记录(message 携带具体原因).
func (l *RemoteOpenLogic) RemoteOpen(req *types.RemoteOpenReq) (*types.RemoteOpenResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	deviceID := strings.TrimSpace(req.DeviceId)
	if deviceID == "" {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "device_id 不能为空")
	}
	operatorID := ctxdata.GetUserId(l.ctx)
	if operatorID == 0 {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "缺少操作人信息(x-user-id)")
	}
	// 参数校验必须早于依赖可用性检查, 否则非法参数会被依赖故障的错误掩盖.
	if l.svcCtx.DeviceRPC == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "M1 设备服务未配置(DeviceRPC)")
	}

	// 权限兜底(M6 CheckPermission 上线前的 interim 方案): 设备不在白名单内一律拒绝.
	// 被拒绝的请求同样要留审计 —— 未授权的开门尝试是安全相关事件.
	if !l.deviceAllowed(deviceID) {
		message := fmt.Sprintf("设备 %s 不在远程开门允许清单内", deviceID)
		l.Slowf("remote open denied: %s operator_id=%d tenant_id=%d", message, operatorID, tenantID)
		l.writeAudit(tenantID, operatorID, deviceID, req.Reason, model.CommandResultFail, message)
		return nil, errorx.NewError(errorx.ErrForbidden, message)
	}

	result, message, err := l.sendCommand(deviceID)

	// 审计写失败绝不能掩盖真正的开门结果, 因此只记日志(命令已下发的事实不因审计失败而改变).
	l.writeAudit(tenantID, operatorID, deviceID, req.Reason, result, message)

	if err != nil {
		l.Errorf("remote open door failed device_id=%s operator_id=%d err=%v", deviceID, operatorID, err)
		return nil, errorx.NewError(errorx.ErrAccessRemoteOpen, message)
	}

	return &types.RemoteOpenResp{Success: true, DeviceId: deviceID, Message: message}, nil
}

// deviceAllowed 判定该设备是否允许被远程开门.
// 采用白名单且 fail-closed: 清单为空时拒绝所有设备, 避免配置遗漏导致内网任何人都能开门.
func (l *RemoteOpenLogic) deviceAllowed(deviceID string) bool {
	for _, id := range l.svcCtx.Config.RemoteOpen.AllowedDeviceIds {
		if strings.TrimSpace(id) == deviceID {
			return true
		}
	}
	return false
}

// sendCommand 向 M1 下发开门指令, 返回 (结果码, 说明, 是否失败).
// 失败时 err 非 nil, 由调用方决定是否对外报错; 结果码用于审计.
func (l *RemoteOpenLogic) sendCommand(deviceID string) (int8, string, error) {
	ctx, cancel := context.WithTimeout(l.ctx, m1CommandTimeout)
	defer cancel()

	res, err := l.svcCtx.DeviceRPC.SendCommand(ctx, &commonpb.DeviceCommand{
		DeviceId: deviceID,
		Command:  commandOpenDoor,
	})
	switch {
	case err != nil:
		if isTimeout(err) {
			return model.CommandResultTimeout, "设备响应超时", err
		}
		return model.CommandResultFail, fmt.Sprintf("调用 M1 SendCommand 失败: %v", err), err
	case res == nil || !res.Success:
		message := "设备返回失败"
		if res != nil && strings.TrimSpace(res.Message) != "" {
			message = res.Message
		}
		return model.CommandResultFail, message, errors.New(message)
	default:
		message := "开门命令已下发"
		if strings.TrimSpace(res.Message) != "" {
			message = res.Message
		}
		return model.CommandResultSuccess, message, nil
	}
}

// writeAudit 落审计流水. 存储未就绪或写入失败仅告警日志: 审计是旁路,
// 阻断主流程会让操作无端失败, 而丢失留痕应由监控发现.
func (l *RemoteOpenLogic) writeAudit(tenantID, operatorID int64, deviceID, reason string, result int8, message string) {
	if l.svcCtx.OperateLogs == nil {
		l.Slowf("remote open audit skipped: mysql not ready device_id=%s", deviceID)
		return
	}
	now := time.Now()
	log := &model.AccessOperateLog{
		DeviceID:   deviceID,
		OperatorID: operatorID,
		Command:    commandOpenDoor,
		Result:     result,
		Message:    message,
		Reason:     reason,
	}
	log.TenantID = tenantID
	log.CreatedAt = now
	log.UpdatedAt = now

	if err := l.svcCtx.OperateLogs.Create(l.ctx, log); err != nil {
		l.Errorf("write access operate log failed device_id=%s result=%d err=%v", deviceID, result, err)
	}
}

// isTimeout 判定是否为超时错误: gRPC 层报 DeadlineExceeded 或本地 ctx 到期.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return status.Code(err) == codes.DeadlineExceeded
}
