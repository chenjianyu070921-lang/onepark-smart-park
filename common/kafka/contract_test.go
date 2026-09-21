package kafka

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDeviceTelemetryRoundTrip 契约结构体 JSON 序列化往返.
func TestDeviceTelemetryRoundTrip(t *testing.T) {
	in := DeviceTelemetry{
		RequestID:  "req-001",
		TenantID:   7,
		DeviceID:   "dev-001",
		DeviceType: "camera",
		EventType:  "intrusion",
		ZoneID:     "zone-a",
		OccurredAt: 1758153600,
		Payload:    json.RawMessage(`{"metrics":{"energy_total":123.5}}`),
		Source:     "mqtt",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	// 关键 JSON 键必须与线上契约一致, 任何键名漂移都会破坏消费方
	for _, key := range []string{"request_id", "tenant_id", "device_id", "device_type", "event_type", "zone_id", "occurred_at", "payload", "source"} {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("缺少 JSON 键 %s: %s", key, b)
		}
	}

	var out DeviceTelemetry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	// json.RawMessage(slice) 使结构体不可整体比较, 逐字段断言
	if out.RequestID != in.RequestID || out.TenantID != in.TenantID || out.DeviceID != in.DeviceID ||
		out.DeviceType != in.DeviceType || out.EventType != in.EventType || out.ZoneID != in.ZoneID ||
		out.OccurredAt != in.OccurredAt || out.Source != in.Source {
		t.Fatalf("往返不一致:\n in=%+v\nout=%+v", in, out)
	}
	if string(out.Payload) != string(in.Payload) {
		t.Fatalf("payload 往返不一致: %s", out.Payload)
	}
}

// TestDeviceTelemetryBackwardCompat 历史消息(无 tenant_id/zone_id)必须能解码为零值,
// 保证契约升级不要求消费方同步发版.
func TestDeviceTelemetryBackwardCompat(t *testing.T) {
	legacy := `{"request_id":"r1","device_id":"d1","device_type":"meter","event_type":"telemetry","occurred_at":100,"payload":{},"source":"mqtt"}`
	var m DeviceTelemetry
	if err := json.Unmarshal([]byte(legacy), &m); err != nil {
		t.Fatalf("历史消息解码失败: %v", err)
	}
	if m.TenantID != 0 || m.ZoneID != "" {
		t.Fatalf("历史消息新字段应为零值, got tenant=%d zone=%q", m.TenantID, m.ZoneID)
	}
	if m.DeviceID != "d1" || m.OccurredAt != 100 {
		t.Fatalf("历史字段解码错误: %+v", m)
	}
}

func TestIsAlarmEvent(t *testing.T) {
	for _, e := range []string{AlarmIntrusion, AlarmFire, AlarmSmoke, AlarmFault, AlarmDoorForce, AlarmOffline} {
		if !IsAlarmEvent(e) {
			t.Fatalf("%s 应为告警事件", e)
		}
	}
	for _, e := range []string{"telemetry", "online", "status", "", "fire_drill"} {
		if IsAlarmEvent(e) {
			t.Fatalf("%s 不应为告警事件", e)
		}
	}
}

// TestNewDLQEnvelopeTruncates 死信信封必须截断超长原始报文, 防止 DLQ 消息本身过大.
func TestNewDLQEnvelopeTruncates(t *testing.T) {
	env := NewDLQEnvelope("onepark/device/pk/d1/event", "报文解析失败", []byte(strings.Repeat("x", 8192)))
	if len(env.Raw) > 4096 {
		t.Fatalf("raw 应截断至 4KB, got %d", len(env.Raw))
	}
	if env.SourceTopic == "" || env.Reason == "" || env.FailedAt.IsZero() {
		t.Fatalf("信封字段不完整: %+v", env)
	}
	if env.FailedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("FailedAt 不应是未来时间: %v", env.FailedAt)
	}
}
