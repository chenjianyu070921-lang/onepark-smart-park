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
	// keyRemoteOpen 远程开门幂等键前缀, 完整键形如 access:remote-open:{tenantID}:{requestID}.
	keyRemoteOpen = "access:remote-open:"
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

	// 幂等: 同一 request_id 重复提交(前端连点/网关重传)只下发一次开门命令.
	// 门被多开一次是不可逆的物理动作, 必须挡在命令下发之前.
	requestID := strings.TrimSpace(req.RequestId)
	duplicate, err := l.isDuplicate(tenantID, requestID)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessRemoteOpen, "幂等校验不可用, 已拒绝开门以避免重复下发")
	}
	if duplicate {
		l.Slowf("remote open skipped duplicate request_id=%s device_id=%s operator_id=%d",
			requestID, deviceID, operatorID)
		return &types.RemoteOpenResp{
			Success:  true,
			DeviceId: deviceID,
			Message:  "重复请求已忽略(同一 request_id 此前已受理, 未重复下发开门命令)",
		}, nil
	}

	// 放行判定: 人员授权优先, 未授权时回退设备白名单(既有兜底), 两者都不满足则拒绝.
	allowed, denyReason := l.authorize(tenantID, operatorID, deviceID)
	if !allowed {
		message := fmt.Sprintf("设备 %s %s", deviceID, denyReason)
		l.Slowf("remote open denied: %s operator_id=%d tenant_id=%d", message, operatorID, tenantID)
		l.writeAudit(tenantID, operatorID, deviceID, req.Reason, model.CommandResultFail, message)
		l.writeRecord(tenantID, operatorID, deviceID, model.AccessResultFail, message)
		// 被拒时一条命令都没下发, 幂等键不能继续占着: 否则调用方带同一 request_id 重试,
		// 会命中上面的幂等分支拿到 {success:true} —— 而门从头到尾没被下发过命令.
		l.releaseDuplicate(tenantID, requestID)
		return nil, errorx.NewError(errorx.ErrForbidden, message)
	}

	result, message, err := l.sendCommand(deviceID)

	// 审计写失败绝不能掩盖真正的开门结果, 因此只记日志(命令已下发的事实不因审计失败而改变).
	l.writeAudit(tenantID, operatorID, deviceID, req.Reason, result, message)
	// 通行记录: 远程开门是一次已发生的通行事实, 成功/失败都要留痕(#48 查询的数据来源之一).
	accessResult := model.AccessResultSuccess
	failReason := ""
	if result != model.CommandResultSuccess {
		accessResult = model.AccessResultFail
		failReason = message
	}
	l.writeRecord(tenantID, operatorID, deviceID, accessResult, failReason)

	if err != nil {
		// 失败/超时: 命令没有成功执行, 释放幂等键让调用方能带同一 request_id 重试真正开门.
		// 不释放的后果比"重复开"更隐蔽: 重试会返回 success=true, 调用方以为门开了.
		l.releaseDuplicate(tenantID, requestID)
		l.Errorf("remote open door failed device_id=%s operator_id=%d err=%v", deviceID, operatorID, err)
		return nil, errorx.NewError(errorx.ErrAccessRemoteOpen, message)
	}

	return &types.RemoteOpenResp{Success: true, DeviceId: deviceID, Message: message}, nil
}

// remoteOpenDedupKey 远程开门幂等键. 必须带 tenant_id: 不同园区可能用到同一个 request_id,
// 不带租户会让 B 园区的请求被 A 园区占用的键误判为"重复".
func remoteOpenDedupKey(tenantID int64, requestID string) string {
	return fmt.Sprintf("%s%d:%s", keyRemoteOpen, tenantID, requestID)
}

// isDuplicate 判定该 request_id 是否已被受理过(预占幂等键).
//
// request_id 为空时不启用幂等: 老调用方不带该字段, 一刀切要求会让存量调用全部变成"无法开门"。
// 幂等组件不可用(Redis 故障)时返回 error, 由调用方拒绝开门 —— 宁可开不了, 也不能重复开.
func (l *RemoteOpenLogic) isDuplicate(tenantID int64, requestID string) (bool, error) {
	if requestID == "" || l.svcCtx.Dedup == nil {
		return false, nil
	}
	key := remoteOpenDedupKey(tenantID, requestID)
	seen, err := l.svcCtx.Dedup.Seen(l.ctx, key)
	if err != nil {
		l.Errorf("remote open dedup unavailable key=%s err=%v", key, err)
		return false, err
	}
	return seen, nil
}

// releaseDuplicate 释放预占的幂等键, 使同一 request_id 可重试.
//
// 只在"命令没有成功下发"时调用(被拒 / M1 失败 / 超时); 成功开门时**绝不能**释放 ——
// 键的存续正是"该 request_id 已真实开过一次门"的凭证.
//
// 释放失败只记日志不改变对外结果: 此时退化为"该 request_id 作废"(与修复前行为一致),
// 比因为清键失败而把一个成功/失败的开门结果改写掉要好.
func (l *RemoteOpenLogic) releaseDuplicate(tenantID int64, requestID string) {
	if requestID == "" || l.svcCtx.Dedup == nil {
		return
	}
	key := remoteOpenDedupKey(tenantID, requestID)
	if err := l.svcCtx.Dedup.Release(l.ctx, key); err != nil {
		l.Errorf("release remote open dedup key failed key=%s err=%v", key, err)
	}
}

// authorize 判定放行, 返回 (是否放行, 不放行时的原因).
//
// 授权(access_permission)此前只写不读 —— 授权表里的数据不产生任何约束力, 等于没落地。
// 这里把它接进放行链路:
//   - 有授权且当前时刻有效 -> 放行(不必在设备白名单内);
//   - 有授权但已过期/不在时段 -> 拒绝: 授权是比白名单更细的控制, 不能让白名单把它盖掉;
//   - 无授权 -> 回退设备白名单(既有兜底, fail-closed).
//
// 权限库查询失败按"无授权"处理并回退白名单: 此时安全性仍由白名单兜底,
// 但必须 Errorf 留痕, 否则"授权校验静默失效"会是一个很难发现的洞.
func (l *RemoteOpenLogic) authorize(tenantID, operatorID int64, deviceID string) (bool, string) {
	if l.svcCtx.Permissions != nil {
		p, err := l.svcCtx.Permissions.FindEffective(l.ctx, tenantID, operatorID, deviceID)
		if err != nil {
			l.Errorf("remote open load permission failed, fall back to device allowlist: "+
				"tenant_id=%d operator_id=%d device_id=%s err=%v", tenantID, operatorID, deviceID, err)
		} else if p != nil {
			ok, reason := PermissionAllowed(p, time.Now())
			if ok {
				return true, ""
			}
			return false, fmt.Sprintf("操作人未被授权开此门(%s)", reason)
		}
	}
	if l.deviceAllowed(deviceID) {
		return true, ""
	}
	return false, "不在远程开门允许清单内且操作人无有效授权"
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

// writeRecord 落通行记录(#48 的数据来源). 与审计同属旁路: 写失败只记日志,
// 不能因为留痕失败让一次已经发生的开门变成"失败".
func (l *RemoteOpenLogic) writeRecord(tenantID, operatorID int64, deviceID string, result int8, failReason string) {
	if l.svcCtx.Records == nil {
		l.Slowf("remote open access record skipped: mysql not ready device_id=%s", deviceID)
		return
	}
	now := time.Now()
	rec := &model.AccessRecord{
		PersonID:   operatorID,
		DeviceID:   deviceID,
		Result:     result,
		OpenType:   model.OpenTypeRemote,
		FailReason: failReason,
		CreatedAt:  now,
	}
	rec.TenantID = tenantID

	if err := l.svcCtx.Records.Create(l.ctx, rec); err != nil {
		l.Errorf("write access record failed device_id=%s result=%d err=%v", deviceID, result, err)
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
