// Package dispatch 负责把 EMQX 收到的设备上报消息分类后投递到 Kafka.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// topic 分段中设备上报类型所在位置: onepark/device/{productKey}/{deviceId}/{kind}
const (
	topicPartDeviceID = 3
	topicPartKind     = 4
	minTopicParts     = 5
)

// 上报类型
const (
	KindEvent     = "event"
	KindTelemetry = "telemetry"
	KindStatus    = "status"
)

// alarmEventTypes 需要额外投递到告警 topic 的事件类型, 与 M3 alarm-service 约定.
var alarmEventTypes = map[string]struct{}{
	"intrusion":     {},
	"fire":          {},
	"smoke":         {},
	"fault":         {},
	"door_force":    {},
	"offline_alert": {},
}

// Message 投递到 Kafka 的消息体.
// 注意: 字段须与 device-service 的 deviceEventMessage 保持一致, 后续应下沉到 common 包共享.
type Message struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"` // mqtt: 经 EMQX 上报
}

// rawMessage 设备上报的原始报文
type rawMessage struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type Handler struct {
	logx.Logger
	producer *kafka.Producer
}

func NewHandler(producer *kafka.Producer) *Handler {
	return &Handler{producer: producer}
}

// Dispatch 解析一条 MQTT 报文并投递到 Kafka.
// topic: onepark/device/{productKey}/{deviceId}/{kind}
func (h *Handler) Dispatch(ctx context.Context, topic string, body []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < minTopicParts {
		return fmt.Errorf("非法的上报 topic: %s", topic)
	}
	kind := parts[topicPartKind]
	topicDeviceID := parts[topicPartDeviceID]

	var raw rawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		// 坏消息: 明确返回错误, 由调用方记录, 不做静默丢弃
		return fmt.Errorf("报文解析失败: %w", err)
	}

	deviceID := raw.DeviceID
	if deviceID == "" {
		deviceID = topicDeviceID
	}
	if deviceID == "" {
		return fmt.Errorf("报文缺少 device_id: topic=%s", topic)
	}

	eventType := raw.EventType
	if eventType == "" {
		// status 类型以 kind 兜底, 如 online/offline 由上报方放在 payload 中
		eventType = kind
	}

	msg := Message{
		RequestID:  raw.RequestID,
		DeviceID:   deviceID,
		DeviceType: raw.DeviceType,
		EventType:  eventType,
		OccurredAt: raw.OccurredAt,
		Payload:    raw.Payload,
		Source:     "mqtt",
	}
	if msg.Payload == nil {
		msg.Payload = json.RawMessage("{}")
	}

	value, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("消息序列化失败: %w", err)
	}

	switch kind {
	case KindEvent, KindTelemetry, KindStatus:
		if err := h.producer.Publish(ctx, kafka.TopicDeviceTelemetry, []byte(deviceID), value); err != nil {
			return fmt.Errorf("投递 %s 失败: %w", kafka.TopicDeviceTelemetry, err)
		}
	default:
		return fmt.Errorf("未知的上报类型: %s", kind)
	}

	// 告警类事件额外投递告警 topic, 供 M3 消费
	if _, ok := alarmEventTypes[eventType]; ok {
		if err := h.producer.Publish(ctx, kafka.TopicAlarm, []byte(deviceID), value); err != nil {
			h.Errorf("投递 %s 失败: deviceId=%s, err=%v", kafka.TopicAlarm, deviceID, err)
		}
	}

	h.Infof("消息已转发: kind=%s, deviceId=%s, eventType=%s", kind, deviceID, eventType)
	return nil
}

// SplitTopic 暴露给单测使用: 返回 topic 中的 deviceId 与 kind.
func SplitTopic(topic string) (deviceID, kind string, ok bool) {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < minTopicParts {
		return "", "", false
	}
	return parts[topicPartDeviceID], parts[topicPartKind], true
}
