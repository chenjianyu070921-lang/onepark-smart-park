// Package frame 定义网关侧 TCP 接入协议与会话处理.
// 帧格式: 以 '\n' 结尾的单行 JSON(便于设备端实现与抓包排查), 单帧上限由配置约束.
package frame

import (
	"bufio"
	"encoding/json"
	"errors"
)

// 帧类型
const (
	TypeAuth      = "auth"      // 设备认证(建连后第一帧)
	TypeTelemetry = "telemetry" // 遥测上报: {"metrics":{"temperature":23.5}}
	TypeEvent     = "event"     // 事件上报: {"event_type":"intrusion","payload":{...}}
	TypeStatus    = "status"    // 状态上报: {"status":"online"}
	TypeAck       = "ack"       // 指令回执: {"request_id":"...","status":"success"}
	TypePing      = "ping"      // 心跳保活
)

// ErrFrameTooLarge 单帧超出上限, 调用方应断开连接.
var ErrFrameTooLarge = errors.New("帧长度超出上限")

// Frame 上行帧.
type Frame struct {
	Type       string         `json:"type"`
	DeviceID   string         `json:"device_id"`
	Secret     string         `json:"secret"`
	RequestID  string         `json:"request_id"`
	DeviceType string         `json:"device_type"`
	EventType  string         `json:"event_type"`
	Metrics    map[string]any `json:"metrics"`
	Payload    map[string]any `json:"payload"`
	Status     string         `json:"status"`
	OccurredAt int64          `json:"occurred_at"`
}

// Response 下行响应帧.
type Response struct {
	OK    bool   `json:"ok"`
	Type  string `json:"type,omitempty"`
	Error string `json:"error,omitempty"`
}

// Message 投递 Kafka 的消息体, 与 event-dispatcher 及 device-service 消费端保持一致.
type Message struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"`
}

// alarmEventTypes 需额外投递告警 topic 的事件类型, 与 event-dispatcher 保持一致.
var alarmEventTypes = map[string]struct{}{
	"intrusion":     {},
	"fire":          {},
	"smoke":         {},
	"fault":         {},
	"door_force":    {},
	"offline_alert": {},
}

// Read 读取一帧, 返回不含换行符的字节切片.
func Read(r *bufio.Reader, maxBytes int) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > maxBytes {
		return nil, ErrFrameTooLarge
	}
	return trimEOL(line), nil
}

func trimEOL(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}
