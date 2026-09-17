package logic

import (
	"context"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// m1OpenDoorTimeout M1 开门 gRPC 短超时(与 visitor-service 签入开门同口径, 避免下游不可用拖死请求).
const m1OpenDoorTimeout = 800 * time.Millisecond

// AccessRemoteOpenLogic 远程开门逻辑: 校验点位 -> 调 M1 SendCommand -> 落通行记录(审计).
type AccessRemoteOpenLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAccessRemoteOpenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AccessRemoteOpenLogic {
	return &AccessRemoteOpenLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AccessRemoteOpen 远程开门: 按点位ID查到 M1 设备并下发开门指令, 成功/失败均落 access_record.
// 错误码口径(单模块验收要求): 缺参 M6-E-0001 / 点位不存在 M6-E-0004 /
// 内部异常 M6-E-0005 / 指令下发失败 M3-E-2003(远程开门失败).
func (l *AccessRemoteOpenLogic) AccessRemoteOpen(req *types.AccessRemoteOpenReq) (resp *types.AccessRemoteOpenResp, err error) {
	// 1) 参数校验: go-zero 的 required 只保证"键存在", 零值需 logic 层拦截(M6-E-0001).
	if req.GateID <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "gate_id 不能为空")
	}
	// 防御: 未配置 MySQL 时提前返回明确错误, 避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrInternal, "数据库未初始化")
	}
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 2) 查点位: 租户隔离内查询, 不存在返回 M6-E-0004.
	var gate model.ParkingGate
	if e := l.svcCtx.DB.WithContext(l.ctx).
		Where("id=? AND tenant_id=?", req.GateID, tenantID).
		First(&gate).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrNotFound, "门禁点位不存在")
		}
		l.Errorf("load parking gate failed: %v", e)
		return nil, errorx.NewError(errorx.ErrInternal, "查询门禁点位失败")
	}
	if gate.Status == model.GateStatusDisabled {
		return nil, errorx.NewError(errorx.ErrForbidden, "门禁点位已停用, 禁止远程开门")
	}

	// 3) 调 M1 下发开门指令(短超时; 未配置/下发失败/设备拒绝均判为开门失败).
	deviceID, openErr := l.sendOpenCommand(gate)

	// 4) 落通行记录: 成功/失败均落库, 审计可溯(验收要求: 数据落库可 SELECT 验证).
	rec := model.AccessRecord{
		TenantID:   tenantID,
		GateID:     gate.ID,
		DeviceID:   deviceID,
		Action:     model.ActionRemoteOpen,
		Result:     model.RecordResultSuccess,
		OperatorID: ctxdata.GetUserId(l.ctx),
		Remark:     req.Remark,
	}
	if openErr != nil {
		rec.Result = model.RecordResultFail
		rec.Remark = failReason(req.Remark, openErr)
	}
	if e := l.svcCtx.DB.WithContext(l.ctx).Create(&rec).Error; e != nil {
		// 开门动作已执行, 审计落库失败仅记日志, 不改变开门结果.
		l.Errorf("create access record failed (gate_id=%d): %v", gate.ID, e)
	}

	if openErr != nil {
		return nil, openErr
	}

	return &types.AccessRemoteOpenResp{
		RecordID: rec.ID,
		GateID:   gate.ID,
		DeviceID: deviceID,
	}, nil
}

// sendOpenCommand 向 M1 device-service 下发开门指令.
// 返回: 实际执行设备ID 与 业务错误(ErrAccessRemoteOpen). M1 未配置/超时/拒绝均返回错误.
func (l *AccessRemoteOpenLogic) sendOpenCommand(gate model.ParkingGate) (string, error) {
	if l.svcCtx.DeviceRPC == nil {
		l.Errorf("remote open failed: device rpc not configured (gate_id=%d)", gate.ID)
		return "", errorx.NewError(errorx.ErrAccessRemoteOpen, "设备网关未配置, 开门失败")
	}

	command := gate.Command
	if command == "" {
		command = "open_door"
	}

	ctx, cancel := context.WithTimeout(l.ctx, m1OpenDoorTimeout)
	defer cancel()
	res, e := l.svcCtx.DeviceRPC.SendCommand(ctx, &commonpb.DeviceCommand{
		DeviceId: gate.DeviceID,
		Command:  command,
	})
	if e != nil {
		l.Errorf("call M1 SendCommand failed (gate_id=%d device_id=%s): %v", gate.ID, gate.DeviceID, e)
		return "", errorx.NewError(errorx.ErrAccessRemoteOpen, "开门指令下发失败")
	}
	if res == nil || !res.Success {
		msg := "设备拒绝开门指令"
		if res != nil && res.Message != "" {
			msg = res.Message
		}
		l.Errorf("M1 SendCommand rejected (gate_id=%d device_id=%s): %s", gate.ID, gate.DeviceID, msg)
		return "", errorx.NewError(errorx.ErrAccessRemoteOpen, msg)
	}

	// 优先使用 M1 返回的实际执行设备ID, 缺省回落到点位配置的 device_id.
	deviceID := gate.DeviceID
	if res.DeviceId != "" {
		deviceID = res.DeviceId
	}
	l.Infof("M1 open door success: gate_id=%d device_id=%s", gate.ID, deviceID)
	return deviceID, nil
}

// failReason 组装失败记录的 remark: 保留操作人事由, 追加失败原因, 便于审计定位.
func failReason(remark string, openErr error) string {
	reason := openErr.Error()
	if ce, ok := openErr.(*errorx.CodeError); ok {
		reason = ce.Msg
	}
	if remark == "" {
		return reason
	}
	return remark + "; " + reason
}
