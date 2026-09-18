// Package dispatch 负责把 EMQX 收到的设备上报消息分类后投递到 Kafka.
// 契约: 消息体使用 common/kafka.DeviceTelemetry(唯一权威定义), 投递前从设备档案
// 充入 tenant_id/zone_id; 坏消息/未知设备/投递重试耗尽统一进死信 topic, 不再静默丢弃.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"onepark/app/event-dispatcher/internal/archive"
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

// defaultRetryPauses 投递失败的退避节奏, 与 alarm-service 消费侧重试一致.
var defaultRetryPauses = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// Publisher Kafka 生产抽象, 便于单测注入假实现; *kafka.Producer 天然满足.
type Publisher interface {
	Publish(ctx context.Context, topic string, key, value []byte) error
}

// rawMessage 设备上报的原始报文.
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
	producer    *kafka.Producer
	publisher   Publisher // 测试注入用, 与 producer 二选一; 非 nil 时优先
	resolver    *archive.Resolver
	retryPauses []time.Duration
}

// NewHandler 构造分发器; resolver 允许为 nil(未配置 MySQL, 消息零值放行).
func NewHandler(producer *kafka.Producer, resolver *archive.Resolver) *Handler {
	return &Handler{
		Logger:      logx.WithContext(context.Background()),
		producer:    producer,
		resolver:    resolver,
		retryPauses: defaultRetryPauses,
	}
}

// publish 统一出口: 优先用注入的 publisher, 否则用真实 producer.
func (h *Handler) publish(ctx context.Context, topic string, key, value []byte) error {
	if h.publisher != nil {
		return h.publisher.Publish(ctx, topic, key, value)
	}
	if h.producer == nil {
		return fmt.Errorf("生产者未初始化")
	}
	return h.producer.Publish(ctx, topic, key, value)
}

// toDLQ 坏消息/耗尽消息投死信; DLQ 投递本身失败仅记日志(最后防线, 不再递归).
func (h *Handler) toDLQ(ctx context.Context, sourceTopic, reason string, raw []byte) {
	env := kafka.NewDLQEnvelope(sourceTopic, reason, raw)
	value, err := json.Marshal(env)
	if err != nil {
		h.Errorf("DLQ 信封序列化失败: %v", err)
		return
	}
	if err := h.publish(ctx, kafka.TopicDispatcherDLQ, nil, value); err != nil {
		h.Errorf("DLQ 投递失败: sourceTopic=%s, reason=%s, err=%v", sourceTopic, reason, err)
	}
}

// publishWithRetry 带退避重试的投递; 重试耗尽进 DLQ.
func (h *Handler) publishWithRetry(ctx context.Context, topic string, key, value []byte, sourceTopic string) {
	var err error
	for attempt := 0; attempt <= len(h.retryPauses); attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryPauses[attempt-1])
		}
		if err = h.publish(ctx, topic, key, value); err == nil {
			return
		}
	}
	h.toDLQ(ctx, sourceTopic, fmt.Sprintf("投递 %s 重试耗尽: %v", topic, err), value)
}

// Dispatch 解析一条 MQTT 报文并投递到 Kafka.
// topic: onepark/device/{productKey}/{deviceId}/{kind}
// 返回的 error 仅表示"本条已无法处理且 DLQ 也失败"等极端情况; 常规坏消息已进 DLQ, 返回 nil.
func (h *Handler) Dispatch(ctx context.Context, topic string, body []byte) error {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < minTopicParts {
		h.toDLQ(ctx, topic, "非法的上报 topic", body)
		return nil
	}
	kind := parts[topicPartKind]
	topicDeviceID := parts[topicPartDeviceID]

	var raw rawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		h.toDLQ(ctx, topic, "报文解析失败: "+err.Error(), body)
		return nil
	}

	deviceID := raw.DeviceID
	if deviceID == "" {
		deviceID = topicDeviceID
	}
	if deviceID == "" {
		h.toDLQ(ctx, topic, "报文缺少 device_id", body)
		return nil
	}

	// 档案充入: tenant_id/zone_id. resolver 为 nil(未配置 MySQL)时零值放行;
	// 已配置但查询失败/设备不存在 → 进 DLQ, 不让无主消息污染下游统计.
	var tenantID int64
	var zoneID string
	if h.resolver != nil {
		p, ok, err := h.resolver.Resolve(ctx, deviceID)
		if err != nil {
			h.toDLQ(ctx, topic, "设备档案查询失败: "+err.Error(), body)
			return nil
		}
		if !ok {
			h.toDLQ(ctx, topic, "未知设备: "+deviceID, body)
			return nil
		}
		tenantID, zoneID = p.TenantID, p.ZoneID
	}

	eventType := raw.EventType
	if eventType == "" {
		// status 类型以 kind 兜底, 如 online/offline 由上报方放在 payload 中
		eventType = kind
	}

	payload := raw.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	msg := kafka.DeviceTelemetry{
		RequestID:  raw.RequestID,
		TenantID:   tenantID,
		DeviceID:   deviceID,
		DeviceType: raw.DeviceType,
		EventType:  eventType,
		ZoneID:     zoneID,
		OccurredAt: raw.OccurredAt,
		Payload:    payload,
		Source:     "mqtt",
	}
	value, err := json.Marshal(msg)
	if err != nil {
		h.toDLQ(ctx, topic, "消息序列化失败: "+err.Error(), body)
		return nil
	}

	switch kind {
	case KindEvent, KindTelemetry, KindStatus:
		h.publishWithRetry(ctx, kafka.TopicDeviceTelemetry, []byte(deviceID), value, topic)
	default:
		h.toDLQ(ctx, topic, "未知的上报类型: "+kind, body)
		return nil
	}

	// 告警类事件额外投递告警 topic, 供 M2 工单/M5 调度消费.
	if kafka.IsAlarmEvent(eventType) {
		h.publishWithRetry(ctx, kafka.TopicAlarm, []byte(deviceID), value, topic)
	}

	h.Infof("消息已转发: kind=%s, deviceId=%s, eventType=%s, tenant=%d, zone=%s",
		kind, deviceID, eventType, tenantID, zoneID)
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
