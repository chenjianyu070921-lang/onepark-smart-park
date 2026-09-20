package svc

import (
	"encoding/json"
	"testing"

	"onepark/common/kafka"
)

// 统一契约回归测试(M1→M3 联调, 2026-09-18 契约定稿):
// 此前消费端本地结构按旧契约解析 area_id/timestamp, 与生产端实际发送的
// zone_id/occurred_at 错配, 表现为静默降级(时间恒 0 → 幂等指纹退化, 区域恒空).
// 用例直接以 common/kafka.DeviceTelemetry(生产端唯一权威结构)序列化作为输入,
// 生产端字段再变动时此处第一时间失败, 而不是线上静默丢字段.

// TestParseDeviceEvent_UnifiedContract 生产端统一契约消息必须完整解析.
func TestParseDeviceEvent_UnifiedContract(t *testing.T) {
	in := kafka.DeviceTelemetry{
		RequestID:  "req-1",
		TenantID:   7,
		DeviceID:   "dev-001",
		DeviceType: "access_control",
		EventType:  kafka.AlarmIntrusion,
		ZoneID:     "zone-a",
		OccurredAt: 1758300000,
		Payload:    []byte(`{"door_status":"open"}`),
		Source:     "mqtt",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("MarshalTelemetry: %v", err)
	}
	ev, err := parseDeviceEvent(raw)
	if err != nil {
		t.Fatalf("parseDeviceEvent: %v", err)
	}
	if ev.RequestID != in.RequestID || ev.TenantID != in.TenantID || ev.DeviceID != in.DeviceID {
		t.Errorf("基础字段解析错误: %+v", ev)
	}
	if ev.EventType != kafka.AlarmIntrusion || ev.DeviceType != in.DeviceType {
		t.Errorf("事件/设备类型解析错误: %+v", ev)
	}
	if ev.ZoneID != "zone-a" {
		t.Errorf("zone_id 未解析到(区域维度规则会全部失效): %+v", ev)
	}
	if ev.EventTime() != in.OccurredAt {
		t.Errorf("occurred_at 未解析到, EventTime=%d want=%d", ev.EventTime(), in.OccurredAt)
	}
	if ev.IdempotentID() != in.RequestID {
		t.Errorf("幂等键应优先取 request_id, got=%s", ev.IdempotentID())
	}
}

// TestParseDeviceEvent_LegacyMessage 契约定稿前的历史消息(area_id/timestamp 毫秒)仍可解析.
func TestParseDeviceEvent_LegacyMessage(t *testing.T) {
	raw := []byte(`{
		"request_id": "req-old",
		"device_id": "dev-002",
		"device_type": "camera",
		"event_type": "intrusion",
		"area_id": 3,
		"timestamp": 1758300000123
	}`)
	ev, err := parseDeviceEvent(raw)
	if err != nil {
		t.Fatalf("parseDeviceEvent: %v", err)
	}
	if ev.AreaID != 3 {
		t.Errorf("旧 area_id 应继续兼容, got=%d", ev.AreaID)
	}
	if got, want := ev.EventTime(), int64(1758300000); got != want {
		t.Errorf("旧 timestamp(毫秒)应换算为秒: got=%d want=%d", got, want)
	}
}

// TestParseDeviceEvent_Malformed 必填字段缺失必须报错, 交由死信兜底.
func TestParseDeviceEvent_Malformed(t *testing.T) {
	cases := map[string]string{
		"非法JSON":      `{not-json`,
		"缺device_id": `{"event_type":"intrusion"}`,
		"缺event_type": `{"device_id":"dev-001"}`,
	}
	for name, body := range cases {
		if _, err := parseDeviceEvent([]byte(body)); err == nil {
			t.Errorf("%s: 期望返回错误", name)
		}
	}
}

// TestIdempotentID_FingerprintUsesEventTime 指纹降级分支必须区分事件时间.
// 修复前 occurred_at 从未解析到, 同设备同类事件的指纹恒相同, 重放会被误判为重复而漏告警.
func TestIdempotentID_FingerprintUsesEventTime(t *testing.T) {
	base := DeviceEvent{
		DeviceID:  "dev-003",
		EventType: kafka.AlarmFire,
	}
	older := base
	older.OccurredAt = 1758300000
	newer := base
	newer.OccurredAt = 1758300100
	if older.IdempotentID() == newer.IdempotentID() {
		t.Error("不同事件时间的指纹不应相同(occurred_at 未参与指纹)")
	}

	// 历史消息仅有 timestamp(毫秒), 换算后与同秒的 occurred_at 指纹一致.
	legacy := base
	legacy.Timestamp = 1758300000123
	if legacy.IdempotentID() != older.IdempotentID() {
		t.Error("旧 timestamp 毫秒换算后应与同秒 occurred_at 指纹一致")
	}

	// request_id 存在时指纹分支不启用.
	withReq := base
	withReq.RequestID = " req-4 "
	if got := withReq.IdempotentID(); got != "req-4" {
		t.Errorf("request_id 应去除空白后直接使用, got=%q", got)
	}
}

// TestRuleFields_ZoneID 规则引擎取值路径 zone_id 必须可达.
func TestRuleFields_ZoneID(t *testing.T) {
	ev := DeviceEvent{
		DeviceID:  "dev-004",
		EventType: kafka.AlarmSmoke,
		ZoneID:    "zone-b",
	}
	f := ev.RuleFields()
	got, ok := f.ResolveField("zone_id")
	if !ok || got != "zone-b" {
		t.Errorf("ResolveField(zone_id) = %v, %v; want zone-b, true", got, ok)
	}
}
