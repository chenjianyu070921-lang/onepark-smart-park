package frame

import (
	"bufio"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReadFrame(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(`{"type":"ping"}` + "\n"))
	line, err := Read(r, 1024)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(line) != `{"type":"ping"}` {
		t.Errorf("帧内容不正确: %q", string(line))
	}
}

// TestReadFrameCRLF 兼容设备端发送 \r\n 的情况.
func TestReadFrameCRLF(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("{\"type\":\"ping\"}\r\n"))
	line, err := Read(r, 1024)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if strings.ContainsAny(string(line), "\r\n") {
		t.Errorf("换行符未被清理: %q", string(line))
	}
}

// TestReadFrameTooLarge 超长帧必须报错, 防止恶意长帧.
func TestReadFrameTooLarge(t *testing.T) {
	long := strings.Repeat("a", 100)
	r := bufio.NewReader(strings.NewReader(long + "\n"))
	if _, err := Read(r, 16); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("期望 ErrFrameTooLarge, 实际 %v", err)
	}
}

func TestFrameParse(t *testing.T) {
	raw := `{"type":"telemetry","device_id":"d1","metrics":{"temperature":23.5}}`
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if f.Type != TypeTelemetry || f.DeviceID != "d1" {
		t.Errorf("字段映射不正确: %+v", f)
	}
	if f.Metrics["temperature"] != 23.5 {
		t.Errorf("metrics 解析不正确: %+v", f.Metrics)
	}
}

func TestResponseMarshal(t *testing.T) {
	b, err := json.Marshal(Response{OK: false, Type: TypeAuth, Error: "密钥校验失败"})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), "密钥校验失败") {
		t.Errorf("响应格式不正确: %s", string(b))
	}
}

// TestMessageContract 保证与 device-service 消费端结构一致.
func TestMessageContract(t *testing.T) {
	msg := Message{
		RequestID: "r1", DeviceID: "d1", DeviceType: "meter",
		EventType: TypeTelemetry, OccurredAt: 1700000000,
		Payload: json.RawMessage(`{"metrics":{"temp":1}}`), Source: "tcp-gateway",
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, key := range []string{`"request_id"`, `"device_id"`, `"event_type"`, `"payload"`, `"source"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("消息缺少字段 %s: %s", key, string(b))
		}
	}
}
