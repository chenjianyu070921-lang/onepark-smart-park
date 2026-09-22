package kafka

import (
	"encoding/json"
	"testing"
)

func TestCommandResultJSONContract(t *testing.T) {
	msg := CommandResult{
		RequestID:  "req-001",
		DeviceID:   "dev-001",
		Status:     CommandStatusFailed,
		Response:   json.RawMessage(`{"error":"motor stalled"}`),
		OccurredAt: 1760000000,
		Source:     "mqtt",
	}

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	wantKeys := []string{
		`"request_id":"req-001"`,
		`"device_id":"dev-001"`,
		`"status":"failed"`,
		`"response":{"error":"motor stalled"}`,
		`"occurred_at":1760000000`,
		`"source":"mqtt"`,
	}
	for _, key := range wantKeys {
		if !jsonContains(string(b), key) {
			t.Fatalf("missing contract field %s in %s", key, b)
		}
	}
}

func TestCommandResultTopicAndGroup(t *testing.T) {
	if TopicDeviceCommandResult != "device-command-result" {
		t.Fatalf("topic = %q, want device-command-result", TopicDeviceCommandResult)
	}
	if GroupDeviceCommandResult != "device-service-command-result" {
		t.Fatalf("group = %q, want device-service-command-result", GroupDeviceCommandResult)
	}
}

func jsonContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
