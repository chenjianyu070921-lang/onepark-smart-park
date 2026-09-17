package logic

import (
	"context"
	"errors"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/app/alarm-service/internal/ws"
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
		l.broadcast(req.Id, tenantID, ws.TypeAlarmAck, target)
	case model.AlarmActionResolve:
		if err := l.svcCtx.Alarms.Resolve(l.ctx, tenantID, req.Id, operatorID, req.Remark, now); err != nil {
			return nil, l.translate(err, errorx.ErrAlarmResolve, "解决告警失败")
		}
		l.broadcast(req.Id, tenantID, ws.TypeAlarmResolved, target)
		// #40: 状态落库后生产 Kafka 事件通知 M5(自动调度闭环)。
		if err := l.notifyResolved(tenantID, req.Id, now); err != nil {
			return nil, err
		}
	}

	return &types.UpdateAlarmStatusResp{Id: req.Id, Status: target}, nil
}

// notifyResolved 生产"告警已解决"事件通知 M5(docs/m3/04 #40)。
//
// 顺序刻意是"先落库、后通知": 通知失败时状态已提交(不回滚), 由 M3-E-1008 提示补偿;
// 若反过来先通知后落库, 一旦落库失败就会出现"M5 已收到、M3 查无此告警"的不一致。
//
// 未配置 Kafka(Notifier 为 nil)时静默跳过: 那是部署时的选择而非运行时故障,
// 启动日志已打印 "alarm event notify to m5 disabled", 此处再逐条告警只会刷日志。
func (l *UpdateAlarmStatusLogic) notifyResolved(tenantID, alarmID int64, at time.Time) error {
	if l.svcCtx.Notifier == nil {
		return nil
	}
	// 事件载荷需要 alarm_no/device_id/level 等字段, 状态流转本身不返回行数据, 故补一次查询。
	a, err := l.svcCtx.Alarms.FindByID(l.ctx, tenantID, alarmID)
	if err != nil {
		l.Errorf("load alarm for notify m5 failed alarm_id=%d err=%v", alarmID, err)
		return errorx.NewError(errorx.ErrAlarmNotify, notifyFailMsg)
	}

	ev := notify.AlarmEvent{
		AlarmID:   a.AlarmNo,
		Action:    notify.ActionResolved,
		AlarmType: a.EventType,
		DeviceID:  a.DeviceID,
		TenantID:  a.TenantID,
		AreaID:    a.AreaID,
		Severity:  a.Level,
		Status:    a.Status,
		Content:   a.Content,
		Timestamp: at.UnixMilli(),
	}
	if err := l.svcCtx.Notifier.AlarmResolved(l.ctx, ev); err != nil {
		l.Errorf("notify m5 alarm resolved failed alarm_no=%s request_id=%s err=%v", a.AlarmNo, a.RequestID, err)
		return errorx.NewError(errorx.ErrAlarmNotify, notifyFailMsg)
	}
	return nil
}

// notifyFailMsg 明确告知调用方"状态已改, 失败的是通知", 避免把重试打在状态流转上。
const notifyFailMsg = "告警已解决, 但通知 M5 失败(状态已落库, 需补偿)"

// broadcast 状态流转后广播(#42). 推送失败不影响接口返回: 状态已落库, 前端重连可拉列表补偿.
func (l *UpdateAlarmStatusLogic) broadcast(alarmID, tenantID int64, typ string, status int8) {
	l.svcCtx.Hub.Push(tenantID, ws.NewEnvelope(typ, ws.AlarmEvent{
		AlarmID: alarmID,
		Status:  status,
	}, ""))
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
