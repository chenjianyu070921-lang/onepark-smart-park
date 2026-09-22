package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/app/alarm-service/internal/ws"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// fakeNotifier 记录发出的告警事件, 可注入错误以验证"通知失败"这条分支.
// 两类动作分开记录: 断言"解决不应发产生事件"这类反向用例时, 共用一条队列会看不出区别.
type fakeNotifier struct {
	resolved []notify.AlarmEvent
	created  []notify.AlarmEvent
	err      error
}

func (f *fakeNotifier) AlarmResolved(_ context.Context, ev notify.AlarmEvent) error {
	if f.err != nil {
		return f.err
	}
	f.resolved = append(f.resolved, ev)
	return nil
}

func (f *fakeNotifier) AlarmCreated(_ context.Context, ev notify.AlarmEvent) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, ev)
	return nil
}

var _ notify.Notifier = (*fakeNotifier)(nil)

// operatorCtx 模拟网关已注入租户与操作人身份.
func operatorCtx(tenantID, userID int64) context.Context {
	return ctxdata.SetUserId(tenantCtx(tenantID), userID)
}

// confirmableAlarm 返回一条"已确认待解决"的告警.
func confirmableAlarm() *model.Alarm {
	return &model.Alarm{
		BaseModel: model.BaseModel{ID: 9001, TenantID: 7, CreatedAt: time.Unix(1757736000, 0)},
		AlarmNo:   "AL2026091699c67157",
		DeviceID:  "door-01",
		AreaID:    12,
		EventType: "intrusion",
		Level:     model.AlarmLevelMajor,
		Status:    model.AlarmStatusResolved,
		Content:   "门禁设备 door-01 检测到非法闯入",
		RequestID: "rid-9001",
	}
}

// newStatusContext 构造状态流转用例上下文(Hub 必须存在: 流转成功后会广播).
func newStatusContext(store *fakeQueryStore, notifier notify.Notifier) *svc.ServiceContext {
	return &svc.ServiceContext{Alarms: store, Notifier: notifier, Hub: ws.NewHub()}
}

// TestUpdateAlarmStatus_ResolveNotifiesM5 #40: 解决告警后生产事件通知 M5, 载荷字段完整.
func TestUpdateAlarmStatus_ResolveNotifiesM5(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}
	notifier := &fakeNotifier{}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, notifier))

	resp, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "resolve", Remark: "现场已处理"})
	if err != nil {
		t.Fatalf("解决告警应成功: %v", err)
	}
	if resp.Id != 9001 || resp.Status != model.AlarmStatusResolved {
		t.Errorf("响应应为已解决状态: %+v", resp)
	}
	if store.resolvedID != 9001 {
		t.Errorf("应调用 Resolve 完成状态流转, 实际 resolvedID=%d", store.resolvedID)
	}
	if len(notifier.resolved) != 1 {
		t.Fatalf("应向 M5 发出 1 条解决事件, 实际 %d 条", len(notifier.resolved))
	}
	// 解决动作只发 resolve, 不得顺带补发 create(那是消费链路的职责).
	if len(notifier.created) != 0 {
		t.Errorf("解决动作不应发出产生事件, 实际 %d 条", len(notifier.created))
	}

	ev := notifier.resolved[0]
	if ev.AlarmID != "AL2026091699c67157" || ev.Action != notify.ActionResolved {
		t.Errorf("事件的幂等键(业务编号)与动作不正确: %+v", ev)
	}
	if ev.DeviceID != "door-01" || ev.AlarmType != "intrusion" || ev.AreaID != 12 || ev.TenantID != 7 {
		t.Errorf("事件定位字段与告警不一致: %+v", ev)
	}
	if ev.Severity != model.AlarmLevelMajor || ev.Status != model.AlarmStatusResolved {
		t.Errorf("事件等级/状态与告警不一致: %+v", ev)
	}
	if ev.Content == "" || ev.Timestamp == 0 {
		t.Errorf("事件内容与时间戳不可为空: %+v", ev)
	}
}

// TestUpdateAlarmStatus_AckDoesNotNotifyM5 确认只是中间态, 不发 M5 事件:
// #39 的契约里没有 Kafka 失败码, 且 M5 对"已确认"没有定义的消费动作.
func TestUpdateAlarmStatus_AckDoesNotNotifyM5(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}
	notifier := &fakeNotifier{}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, notifier))

	resp, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "ack"})
	if err != nil {
		t.Fatalf("确认告警应成功: %v", err)
	}
	if resp.Status != model.AlarmStatusAcked || store.ackedID != 9001 {
		t.Errorf("确认流程异常: resp=%+v ackedID=%d", resp, store.ackedID)
	}
	if len(notifier.resolved) != 0 || len(notifier.created) != 0 {
		t.Errorf("确认不应向 M5 发事件, 实际 resolve=%d create=%d", len(notifier.resolved), len(notifier.created))
	}
}

// TestUpdateAlarmStatus_NotifyFailedKeepsResolvedState 通知失败: 状态保持已解决, 返回专属错误码提示补偿.
func TestUpdateAlarmStatus_NotifyFailedKeepsResolvedState(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8),
		newStatusContext(store, &fakeNotifier{err: errors.New("kafka down")}))

	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "resolve"})
	assertCode(t, err, errorx.ErrAlarmNotify)
	if store.resolvedID != 9001 {
		t.Errorf("通知失败不得回滚状态流转(已落库), 实际 resolvedID=%d", store.resolvedID)
	}
}

// TestUpdateAlarmStatus_NotifyLoadAlarmFailed 解决成功但读不到告警行(用于组装事件)时同样按通知失败返回.
func TestUpdateAlarmStatus_NotifyLoadAlarmFailed(t *testing.T) {
	store := &fakeQueryStore{} // detail 为 nil: FindByID 返回 ErrNotFound
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, &fakeNotifier{}))

	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "resolve"})
	assertCode(t, err, errorx.ErrAlarmNotify)
	if store.resolvedID != 9001 {
		t.Errorf("状态流转应已完成: resolvedID=%d", store.resolvedID)
	}
}

// TestUpdateAlarmStatus_NotifierDisabled 未配置 Kafka 时跳过通知, 不影响解决动作本身.
func TestUpdateAlarmStatus_NotifierDisabled(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, nil))

	resp, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "resolve"})
	if err != nil {
		t.Fatalf("未配置通知器不应影响解决: %v", err)
	}
	if resp.Status != model.AlarmStatusResolved || store.resolvedID != 9001 {
		t.Errorf("解决流程异常: resp=%+v resolvedID=%d", resp, store.resolvedID)
	}
}

// TestUpdateAlarmStatus_TransitionFailed 状态流转失败(不存在/前置状态不符)时不得发通知.
func TestUpdateAlarmStatus_TransitionFailed(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm(), transition: model.ErrNotFound}
	notifier := &fakeNotifier{}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, notifier))

	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "resolve"})
	assertCode(t, err, errorx.ErrAlarmStatusInvalid)
	if len(notifier.resolved) != 0 || len(notifier.created) != 0 {
		t.Errorf("流转失败不应通知 M5, 实际 resolve=%d create=%d", len(notifier.resolved), len(notifier.created))
	}
}

// TestUpdateAlarmStatus_ParamInvalid 非法动作必须早于依赖检查被拦下, 且不触达存储.
func TestUpdateAlarmStatus_ParamInvalid(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), newStatusContext(store, &fakeNotifier{}))

	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "bogus"})
	assertCode(t, err, errorx.ErrAlarmParamInvalid)
	if store.ackedID != 0 || store.resolvedID != 0 {
		t.Error("非法动作不应触达存储")
	}
}

// TestUpdateAlarmStatus_MissingIdentity 缺少租户/操作人时拒绝操作(操作审计必须有操作人).
// 断言 M3-W-1001 而非 M6-E-0001(KI-2): 两者都表示"调用方没传 header",
// 但后者被 HttpStatus 映射成 500, 前端会把参数问题当成服务端故障重试.
func TestUpdateAlarmStatus_MissingIdentity(t *testing.T) {
	store := &fakeQueryStore{detail: confirmableAlarm()}

	l := NewUpdateAlarmStatusLogic(operatorCtx(0, 8), newStatusContext(store, &fakeNotifier{}))
	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "ack"})
	assertCode(t, err, errorx.ErrAlarmParamInvalid)

	l = NewUpdateAlarmStatusLogic(operatorCtx(7, 0), newStatusContext(store, &fakeNotifier{}))
	_, err = l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "ack"})
	assertCode(t, err, errorx.ErrAlarmParamInvalid)
}

// TestUpdateAlarmStatus_StorageUnavailable 存储未就绪时返回依赖故障码.
func TestUpdateAlarmStatus_StorageUnavailable(t *testing.T) {
	l := NewUpdateAlarmStatusLogic(operatorCtx(7, 8), &svc.ServiceContext{Hub: ws.NewHub()})

	_, err := l.UpdateAlarmStatus(&types.UpdateAlarmStatusReq{Id: 9001, Action: "ack"})
	assertCode(t, err, errorx.ErrDepConnect)
}
