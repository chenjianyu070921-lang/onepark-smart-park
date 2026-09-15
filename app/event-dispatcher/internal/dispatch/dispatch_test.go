package dispatch

import (
	"context"
	"testing"
)

func TestSplitTopic(t *testing.T) {
	tests := []struct {
		name         string
		topic        string
		wantDeviceID string
		wantKind     string
		wantOK       bool
	}{
		{"事件上报", "onepark/device/pk001/dev-001/event", "dev-001", "event", true},
		{"遥测上报", "onepark/device/pk001/dev-002/telemetry", "dev-002", "telemetry", true},
		{"上下线带斜杠", "/onepark/device/pk001/dev-003/status/", "dev-003", "status", true},
		{"层级不足", "onepark/device/pk001", "", "", false},
		{"空 topic", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceID, kind, ok := SplitTopic(tt.topic)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if deviceID != tt.wantDeviceID {
				t.Errorf("deviceID = %s, 期望 %s", deviceID, tt.wantDeviceID)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %s, 期望 %s", kind, tt.wantKind)
			}
		})
	}
}

// TestDispatchInvalidTopic 非法 topic 应返回错误且不得 panic(producer 为 nil 时不会走到投递).
func TestDispatchInvalidTopic(t *testing.T) {
	h := NewHandler(nil)
	if err := h.Dispatch(context.Background(), "onepark/device/pk001", []byte(`{}`)); err == nil {
		t.Fatal("期望非法 topic 返回错误")
	}
}
