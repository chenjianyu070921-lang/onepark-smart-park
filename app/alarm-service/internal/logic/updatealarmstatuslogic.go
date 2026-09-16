package logic

import (
	"context"
	"errors"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// UpdateAlarmStatusLogic 告警状态流转逻辑: ack(未处理→已确认) / resolve(已确认→已解决).
// 状态机禁止跨级流转(未处理不可直接解决), 并发冲突由 Update 的 RowsAffected 判定.
type UpdateAlarmStatusLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateAlarmStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateAlarmStatusLogic {
	return &UpdateAlarmStatusLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateAlarmStatusLogic) UpdateAlarmStatus(req *types.UpdateAlarmStatusReq) (*types.UpdateAlarmStatusResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	operatorID := ctxdata.GetUserId(l.ctx)
	if operatorID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少操作人信息(x-user-id)")
	}
	// 参数校验必须早于存储就绪检查, 否则非法参数会被 500(M6-E-0006) 掩盖成依赖故障.
	target, ok := actionTarget(req.Action)
	if !ok {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "action 仅支持 ack(确认) / resolve(解决)")
	}
	if l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置)")
	}

	now := time.Now()
	switch req.Action {
	case model.AlarmActionAck:
		if err := l.svcCtx.Alarms.Ack(l.ctx, tenantID, req.Id, operatorID, req.Remark, now); err != nil {
			return nil, l.translate(err, errorx.ErrAlarmAck, "确认告警失败")
		}
	case model.AlarmActionResolve:
		if err := l.svcCtx.Alarms.Resolve(l.ctx, tenantID, req.Id, operatorID, req.Remark, now); err != nil {
			return nil, l.translate(err, errorx.ErrAlarmResolve, "解决告警失败")
		}
	}

	return &types.UpdateAlarmStatusResp{Id: req.Id, Status: target}, nil
}

// actionTarget 返回动作对应的目标状态; 非法动作返回 false.
func actionTarget(action string) (int8, bool) {
	switch action {
	case model.AlarmActionAck:
		return model.AlarmStatusAcked, true
	case model.AlarmActionResolve:
		return model.AlarmStatusResolved, true
	default:
		return 0, false
	}
}

// translate 将持久层错误转译为业务错误码.
// ErrNotFound 既可能是告警不存在, 也可能是前置状态不匹配(并发竞争), 统一按状态不允许处理;
// 其余失败按动作区分码值(确认/解决各有专属错误码, 见 common/errorx).
func (l *UpdateAlarmStatusLogic) translate(err error, code, msg string) error {
	if errors.Is(err, model.ErrNotFound) {
		return errorx.NewError(errorx.ErrAlarmStatusInvalid, "告警不存在或当前状态不允许该操作")
	}
	l.Errorf("update alarm status failed: %v", err)
	return errorx.NewError(code, msg)
}
