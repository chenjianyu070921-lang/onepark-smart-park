// Package ws 提供告警实时推送的 WebSocket 连接管理与广播(docs/m3/09).
//
// 核心约束(与 M5 dashboard-service/internal/wshub 同构):
// gorilla/websocket 同一连接不允许并发写, 并发写会直接 panic。
// 因此每个连接独享一个 writePump goroutine + 带缓冲的发送通道,
// 广播方只往通道塞消息, 真正写 socket 的永远只有这一个 goroutine。
//
// 与 M5 的差异: 告警按园区隔离, 广播必须按 tenant_id 过滤(见 Hub.BroadcastTo)。
package ws

import (
	"encoding/json"
	"time"
)

// 推送消息类型(docs/m3/09 §5).
const (
	TypeAlarmCreated  = "alarm.created"
	TypeAlarmAck      = "alarm.ack"
	TypeAlarmResolved = "alarm.resolved"
)

// Envelope 推送消息信封.
type Envelope struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Ts        int64       `json:"ts"`
	RequestId string      `json:"request_id"`
}

// AlarmEvent 告警事件载荷(docs/m3/09 §5).
// AlarmID 为业务编号 alarm_no(string), 与 notify.AlarmEvent 对齐:
// WebSocket 推送与 Kafka 通知必须使用相同的告警标识, 否则前端与 M5 收到不同的值.
type AlarmEvent struct {
	AlarmID   string `json:"alarm_id"`
	DeviceID  string `json:"device_id"`
	EventType string `json:"event_type"`
	Level     int8   `json:"level"`
	Content   string `json:"content"`
	AreaID    int64  `json:"area_id"`
	Status    int8   `json:"status"`
}

// NewEnvelope 构造信封; 空 requestId 时留空串(上游未透传不代表异常).
func NewEnvelope(typ string, data interface{}, requestID string) *Envelope {
	return &Envelope{Type: typ, Data: data, Ts: time.Now().Unix(), RequestId: requestID}
}

// Encode 序列化消息.
// 注意: 不可把本方法命名为 MarshalJSON —— 那样 json.Marshal 会回调它形成无限递归.
func (e *Envelope) Encode() ([]byte, error) {
	return json.Marshal(e)
}
