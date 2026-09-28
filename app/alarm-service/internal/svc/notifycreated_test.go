package svc

import (
	"context"
	"errors"
	"testing"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
)

// 本文件覆盖 #40 的「告警产生」事件与事件时间口径, 与 M1 统一契约回归用例
// (consumer_test.go)分开存放: 后者由契约变更驱动, 前者由本服务行为驱动.

// TestEventTime_UnifiedContractSeconds 事件时间必须是 Unix 秒, 并兼容历史毫秒字段.
//
// 单位必须与 common/kafka.DeviceTelemetry 一致: 告警落库、ES 双写、大屏聚合都按秒消费,
// 混入毫秒会让时间整体偏移 1000 倍 —— 这类错误不报错, 表现为"告警时间全是未来/1970"。
func TestEventTime_UnifiedContractSeconds(t *testing.T) {
	cases := []struct {
		name string
		ev   DeviceEvent
		want int64
	}{
		{"统一契约 occurred_at 优先", DeviceEvent{OccurredAt: 1758300000, Timestamp: 1758300123456}, 1758300000},
		{"历史消息仅 timestamp 毫秒换算为秒", DeviceEvent{Timestamp: 1758300000000}, 1758300000},
		{"两者都缺返回 0", DeviceEvent{}, 0},
	}
	for _, c := range cases {
		if got := c.ev.EventTime(); got != c.want {
			t.Errorf("%s: EventTime() = %d, 期望 %d", c.name, got, c.want)
		}
	}
}

// TestEventTime_ZeroKeepsFingerprintStable 时间缺失时返回 0 而不是当前时间.
// 若用"处理时刻"兜底, 同一条消息重投会算出不同指纹, 幂等直接失效。
func TestEventTime_ZeroKeepsFingerprintStable(t *testing.T) {
	a := DeviceEvent{DeviceID: "d1", EventType: "intrusion"}
	b := DeviceEvent{DeviceID: "d1", EventType: "intrusion"}
	if a.IdempotentID() != b.IdempotentID() {
		t.Errorf("事件时间缺失时指纹必须稳定: %s vs %s", a.IdempotentID(), b.IdempotentID())
	}
}

// TestHandleDeviceEvent_NotifiesM5AlarmCreated 告警落库后投递"产生"事件, 载荷与告警一致.
func TestHandleDeviceEvent_NotifiesM5AlarmCreated(t *testing.T) {
	store := &fakeAlarmStore{}
	notifier := &fakeEventNotifier{}
	svcCtx := &ServiceContext{
		Alarms:   store,
		Dedup:    &fakeDeduper{seen: map[string]bool{}},
		Notifier: notifier,
	}

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-notify")); err != nil {
		t.Fatalf("处理出错: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("应落库 1 条告警, 实际 %d 条", len(store.alarms))
	}
	if len(notifier.created) != 1 {
		t.Fatalf("应向 M5 发出 1 条产生事件, 实际 %d 条", len(notifier.created))
	}
	if len(notifier.resolved) != 0 {
		t.Errorf("不应发出解决事件, 实际 %d 条", len(notifier.resolved))
	}

	a, ev := store.alarms[0], notifier.created[0]
	if ev.Action != notify.ActionCreated {
		t.Errorf("动作应为 create, 实际 %q", ev.Action)
	}
	if ev.AlarmID != a.AlarmNo {
		t.Errorf("事件幂等键应取业务编号: event=%s alarm=%s", ev.AlarmID, a.AlarmNo)
	}
	if ev.Severity != a.Level || ev.AlarmType != a.EventType || ev.DeviceID != a.DeviceID {
		t.Errorf("事件载荷与告警不一致: %+v vs %+v", ev, a)
	}
	if ev.Status != model.AlarmStatusPending {
		t.Errorf("产生事件状态应为未处理(%d), 实际 %d", model.AlarmStatusPending, ev.Status)
	}
}

// TestHandleDeviceEvent_NotifyCreatedFailureDoesNotDropAlarm 通知失败: 告警保留、位移照常提交.
//
// 锁死"通知失败 ≠ 丢告警": 告警已落库后返回 error 只会让消费端重投,
// 而重投会被 L1/L3 幂等拦下 —— 通知永远补不上, 却把消息推进死信。
func TestHandleDeviceEvent_NotifyCreatedFailureDoesNotDropAlarm(t *testing.T) {
	store := &fakeAlarmStore{}
	svcCtx := &ServiceContext{
		Alarms:   store,
		Dedup:    &fakeDeduper{seen: map[string]bool{}},
		Notifier: &fakeEventNotifier{err: errors.New("kafka down")},
	}

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-notify-fail")); err != nil {
		t.Fatalf("通知失败不应让消费链路报错(否则会重投/进死信): %v", err)
	}
	if len(store.alarms) != 1 {
		t.Errorf("通知失败不得影响告警落库, 实际 %d 条", len(store.alarms))
	}
}

// TestHandleDeviceEvent_NotifierDisabledSkipsNotify 未配置 Kafka 时跳过通知, 告警照常落库.
func TestHandleDeviceEvent_NotifierDisabledSkipsNotify(t *testing.T) {
	svcCtx, store := newTestContext() // Notifier 为 nil
	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-no-notify")); err != nil {
		t.Fatalf("处理出错: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Errorf("未配置通知器不应影响落库, 实际 %d 条", len(store.alarms))
	}
}
