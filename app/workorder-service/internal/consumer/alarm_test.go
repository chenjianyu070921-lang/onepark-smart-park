package consumer

import (
	"encoding/json"
	"strings"
	"testing"
)

// validAlarm 构造一条合法告警消息.
func validAlarm(t *testing.T, override map[string]interface{}) []byte {
	t.Helper()
	m := map[string]interface{}{
		"request_id":  "alarm-req-001",
		"device_id":   "dev-smoke-01",
		"device_type": "smoke_detector",
		"event_type":  "smoke",
		"occurred_at": 1726000000,
		"source":      "m1-device",
		"payload":     map[string]interface{}{"zone_code": "A-3F", "tenant_id": 2},
	}
	for k, v := range override {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal alarm: %v", err)
	}
	return b
}

func TestDecodeAlarm_OK(t *testing.T) {
	evt, err := DecodeAlarm(validAlarm(t, nil))
	if err != nil {
		t.Fatalf("expect nil err, got %v", err)
	}
	if evt.RequestID != "alarm-req-001" || evt.DeviceID != "dev-smoke-01" || evt.EventType != "smoke" {
		t.Fatalf("unexpected event: %+v", evt)
	}
}

func TestDecodeAlarm_MissingRequestID(t *testing.T) {
	// request_id 是幂等键, 缺失必须拒绝, 否则同一告警可能重复建单.
	if _, err := DecodeAlarm(validAlarm(t, map[string]interface{}{"request_id": ""})); err == nil {
		t.Fatal("expect error for missing request_id, got nil")
	}
}

func TestDecodeAlarm_MissingDeviceID(t *testing.T) {
	if _, err := DecodeAlarm(validAlarm(t, map[string]interface{}{"device_id": ""})); err == nil {
		t.Fatal("expect error for missing device_id, got nil")
	}
}

func TestDecodeAlarm_BadJSON(t *testing.T) {
	if _, err := DecodeAlarm([]byte("not-json")); err == nil {
		t.Fatal("expect error for invalid json, got nil")
	}
}

func TestBuildRepairDraft_KnownEventType(t *testing.T) {
	evt, _ := DecodeAlarm(validAlarm(t, nil))
	draft := BuildRepairDraft(evt, 1)

	if draft.Type != 1 { // 报修
		t.Fatalf("expect repair type 1, got %d", draft.Type)
	}
	if draft.Priority != 1 { // smoke → 紧急
		t.Fatalf("expect urgent priority 1, got %d", draft.Priority)
	}
	if draft.Title != "烟雾告警处置" {
		t.Fatalf("unexpected title: %s", draft.Title)
	}
	if draft.TenantID != 2 { // payload 携带的租户优先
		t.Fatalf("expect tenant 2 from payload, got %d", draft.TenantID)
	}
	if draft.Location != "A-3F" {
		t.Fatalf("expect zone location, got %q", draft.Location)
	}
	if draft.AlarmID != "alarm-req-001" {
		t.Fatalf("expect alarm id from request_id, got %s", draft.AlarmID)
	}
	if !strings.Contains(draft.Description, "dev-smoke-01") {
		t.Fatalf("description should mention device, got %s", draft.Description)
	}
}

func TestBuildRepairDraft_UnknownEventType(t *testing.T) {
	// 未知告警类型不能丢弃, 按普通优先级落单.
	evt, _ := DecodeAlarm(validAlarm(t, map[string]interface{}{"event_type": "unknown_xx"}))
	draft := BuildRepairDraft(evt, 1)
	if draft.Priority != 2 {
		t.Fatalf("expect normal priority for unknown type, got %d", draft.Priority)
	}
	if !strings.Contains(draft.Title, "unknown_xx") {
		t.Fatalf("title should carry raw event type, got %s", draft.Title)
	}
}

func TestBuildRepairDraft_DefaultTenant(t *testing.T) {
	// payload 未携带租户时使用配置兜底, 且不允许 <=0.
	evt, _ := DecodeAlarm(validAlarm(t, map[string]interface{}{
		"payload": map[string]interface{}{"zone_code": "B-1F"},
	}))
	draft := BuildRepairDraft(evt, 3)
	if draft.TenantID != 3 {
		t.Fatalf("expect default tenant 3, got %d", draft.TenantID)
	}
}

func TestBuildRepairDraft_TenantFallbackWhenNonPositive(t *testing.T) {
	evt, _ := DecodeAlarm(validAlarm(t, map[string]interface{}{
		"payload": map[string]interface{}{"tenant_id": 0},
	}))
	draft := BuildRepairDraft(evt, 7)
	if draft.TenantID != 7 {
		t.Fatalf("expect fallback tenant 7, got %d", draft.TenantID)
	}
}

func TestBuildRepairDraft_LocationOrder(t *testing.T) {
	// 位置取值顺序: zone_code 优先, 其次 location, 都没有则留空(不臆造).
	evt, _ := DecodeAlarm(validAlarm(t, map[string]interface{}{
		"payload": map[string]interface{}{"location": "3号楼东侧"},
	}))
	if got := BuildRepairDraft(evt, 1).Location; got != "3号楼东侧" {
		t.Fatalf("expect location fallback, got %q", got)
	}
	evt2, _ := DecodeAlarm(validAlarm(t, map[string]interface{}{"payload": nil}))
	if got := BuildRepairDraft(evt2, 1).Location; got != "" {
		t.Fatalf("expect empty location, got %q", got)
	}
}
