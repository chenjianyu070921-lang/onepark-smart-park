// 契约定稿(2026-09-18): 设备遥测/事件消息的唯一权威定义.
// 此前 event-dispatcher、device-service、gateway-service、workorder/dispatch 消费端
// 各自复制结构体, 已实际造成 M4 按错误想象编写消费端(错误 topic 名+错误字段结构),
// 因此下沉到 common 作为编译期约束; 各服务不得再本地定义同名结构.
package kafka

import (
	"encoding/json"
	"time"
)

// DeviceTelemetry 设备遥测/事件统一契约.
// topic: TopicDeviceTelemetry(全量上报); TopicAlarm(仅 IsAlarmEvent 命中的告警类).
// 兼容性: 与 2026-09 前线上格式逐字段一致, 仅新增 tenant_id/zone_id;
// 历史消息缺省解码为零值, 消费方无需同步升级.
type DeviceTelemetry struct {
	RequestID  string          `json:"request_id"`
	TenantID   int64           `json:"tenant_id"` // 生产端充入, 0 表示未归属(历史设备/未配置档案查询)
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	ZoneID     string          `json:"zone_id"`     // 能源区域编码(M4 计费/分析维度), 空表示未分区
	OccurredAt int64           `json:"occurred_at"` // Unix 秒; 不得改为 time.Time, 会破坏现有消费方
	Payload    json.RawMessage `json:"payload"`     // 业务负载, 遥测类为 {"metrics":{...}}
	Source     string          `json:"source"`      // tcp-gateway / mqtt / http-fallback
}

// 事件类型常量(status 类事件的取值, 同时兜底 MQTT topic 的 kind 段).
const (
	EventOnline    = "online"
	EventOffline   = "offline"
	EventFault     = "fault"
	EventStatus    = "status"
	EventTelemetry = "telemetry"
	EventGeneric   = "event"
)

// 告警类事件类型: 命中后消息会额外投递 TopicAlarm, 供 M2 工单/M5 调度消费.
// 此前在 dispatch.go / deviceeventlogic.go / gateway protocol.go 三处硬编码, 现合一.
const (
	AlarmIntrusion = "intrusion"     // 非法入侵
	AlarmFire      = "fire"          // 火情
	AlarmSmoke     = "smoke"         // 烟感
	AlarmFault     = "fault"         // 设备故障
	AlarmDoorForce = "door_force"    // 门禁强开
	AlarmOffline   = "offline_alert" // 异常离线
)

var alarmEventTypes = map[string]struct{}{
	AlarmIntrusion: {},
	AlarmFire:      {},
	AlarmSmoke:     {},
	AlarmFault:     {},
	AlarmDoorForce: {},
	AlarmOffline:   {},
}

// IsAlarmEvent 判断事件类型是否为告警类.
func IsAlarmEvent(eventType string) bool {
	_, ok := alarmEventTypes[eventType]
	return ok
}

// DLQEnvelope 死信信封: 坏消息/投递重试耗尽的消息统一包装后投 TopicDispatcherDLQ,
// 供事后排查与重放; 不再静默丢弃.
type DLQEnvelope struct {
	SourceTopic string    `json:"source_topic"` // 来源 MQTT topic 或目标 Kafka topic
	Reason      string    `json:"reason"`       // 解析失败 / 未知设备 / 档案查询失败 / 投递重试耗尽
	Raw         string    `json:"raw"`          // 原始报文, 截断至 4KB
	FailedAt    time.Time `json:"failed_at"`
}

// maxDLQRawBytes 死信原始报文上限, 防止 DLQ 消息本身过大撑爆分区.
const maxDLQRawBytes = 4096

// NewDLQEnvelope 构造死信信封, raw 超长时截断.
func NewDLQEnvelope(sourceTopic, reason string, raw []byte) DLQEnvelope {
	if len(raw) > maxDLQRawBytes {
		raw = raw[:maxDLQRawBytes]
	}
	return DLQEnvelope{
		SourceTopic: sourceTopic,
		Reason:      reason,
		Raw:         string(raw),
		FailedAt:    time.Now(),
	}
}
