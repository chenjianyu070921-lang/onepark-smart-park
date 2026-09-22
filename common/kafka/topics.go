// Package kafka 提供 Kafka 生产者/消费者基础封装与主题常量, 供各业务服务复用.
// 采用 segmentio/kafka-go 实现, 支持按 broker 列表初始化、按 topic/group 消费.
// 设计约定: 业务服务之间事件驱动通过 Kafka 解耦(停车地磁事件、告警联动、公告推送等).
package kafka

// 主题常量: 统一命名, 避免散落硬编码.
const (
	TopicDeviceTelemetry = "device-telemetry" // M1 设备遥测(地磁/门禁上报)
	TopicParkingEntry    = "parking-entry"    // 车辆入场事件(parking-service 发布)
	TopicParkingExit     = "parking-exit"     // 车辆离场事件(parking-service 发布)
	TopicAlarm           = "alarm-event"      // 告警事件(M1 event-dispatcher / parking 生产, M5 dispatch 消费)
	// TopicAlarmEvent M3 alarm-service 生产 → M5 消费 的告警事件(docs/m3/04 §3.2; M5 确认书 §5 提案).
	// 与 TopicAlarm 分开是刻意的: TopicAlarm 承载 M1 的原始告警(含 request_id/occurred_at, 供自动建单),
	// 本 topic 承载 M3 的告警生命周期事件(alarm_id/severity/content), 两者结构不同;
	// 混在同一条 topic 上会让消费方的解码逻辑互相打架(缺字段即丢消息).
	TopicAlarmEvent = "onepark.alarm.event"
	TopicNotice     = "notice-event"    // 公告发布事件(通知类消费)
	TopicWorkorder  = "workorder-event" // 工单状态事件(workorder-service 发布, M5/通知类消费)
	TopicVisitor    = "visitor-event"   // 访客事件(visitor-service 发布 invite/checkin/checkout, 大屏等消费方)

	// TopicDispatcherDLQ event-dispatcher 死信 topic: 坏消息/未知设备/投递重试耗尽的消息信封,
	// 供排查与重放, 不再静默丢弃.
	TopicDispatcherDLQ = "event-dispatcher-dlq"
)

// 消费者组常量(同一服务多实例共享消费位移).
const (
	GroupParking   = "parking-service-v2" // P0-2 临时: 绕过协调故障损坏的原 group, 验证后回退
	GroupAlarm     = "alarm-service"
	GroupNotice    = "notice-service"
	GroupWorkorder = "workorder-service"
	GroupDevice    = "device-service" // M1 遥测回写(设备状态/影子/时序库)
	GroupVideo     = "video-service"  // M3 摄像头心跳(更新 camera.last_heartbeat_at / status)
)

// TopicDeviceCommandResult carries device command acknowledgements from
// event-dispatcher and gateway-service back to device-service.
const TopicDeviceCommandResult = "device-command-result"

// GroupDeviceCommandResult is the consumer group used by device-service to persist
// command acknowledgements.
const GroupDeviceCommandResult = "device-service-command-result"
