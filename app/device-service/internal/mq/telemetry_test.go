package mq

import (
	"context"
	"encoding/json"
	"testing"

	"onepark/app/device-service/internal/svc"
	"onepark/common/kafka"
)

// TestHandleBadMessage 坏消息与缺 device_id 的消息必须丢弃(返回 nil),
// 否则消费端会卡在同一条消息上反复重投.
func TestHandleBadMessage(t *testing.T) {
	h := NewHandler(&svc.ServiceContext{})

	if err := h.Handle(context.Background(), kafka.Message{Value: []byte("not-json")}); err != nil {
		t.Errorf("坏消息应丢弃, 实际返回: %v", err)
	}
	if err := h.Handle(context.Background(), kafka.Message{
		Value: []byte(`{"event_type":"telemetry"}`),
	}); err != nil {
		t.Errorf("缺少 device_id 应丢弃, 实际返回: %v", err)
	}
}

func TestIsStatusEvent(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want bool
	}{
		{"event_type=online", Message{EventType: "online"}, true},
		{"event_type=offline", Message{EventType: "offline"}, true},
		{"event_type=status", Message{EventType: "status"}, true},
		{
			"payload.online=true",
			Message{EventType: "telemetry", Payload: json.RawMessage(`{"online":true}`)}, true,
		},
		{
			"payload.status=fault",
			Message{EventType: "event", Payload: json.RawMessage(`{"status":"fault"}`)}, true,
		},
		{
			"遥测无状态字段",
			Message{EventType: "telemetry", Payload: json.RawMessage(`{"metrics":{"temp":1}}`)}, false,
		},
		{"门磁事件", Message{EventType: "door_force"}, false},
	}

	for _, c := range cases {
		if got := isStatusEvent(c.msg); got != c.want {
			t.Errorf("%s: 期望 %v, 实际 %v", c.name, c.want, got)
		}
	}
}

func TestToFloat(t *testing.T) {
	if v, ok := toFloat(23.5); !ok || v != 23.5 {
		t.Errorf("float64 转换失败: %v %v", v, ok)
	}
	if v, ok := toFloat(10); !ok || v != 10 {
		t.Errorf("int 转换失败: %v %v", v, ok)
	}
	if v, ok := toFloat("12.5"); !ok || v != 12.5 {
		t.Errorf("string 转换失败: %v %v", v, ok)
	}
	if _, ok := toFloat(map[string]any{}); ok {
		t.Error("非法类型应返回 false")
	}
}

// TestMessageContract 保证与 event-dispatcher 投递结构一致, 字段漂移时单测先失败.
func TestMessageContract(t *testing.T) {
	raw := []byte(`{"request_id":"r1","device_id":"d1","device_type":"meter",
		"event_type":"telemetry","occurred_at":1700000000,"payload":{"metrics":{"temp":1}},"source":"mqtt"}`)
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m.RequestID != "r1" || m.DeviceID != "d1" || m.EventType != "telemetry" || m.Source != "mqtt" {
		t.Errorf("字段映射不一致: %+v", m)
	}
}
