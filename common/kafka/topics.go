// Package kafka 提供 Kafka 生产者/消费者基础封装与主题常量, 供各业务服务复用.
// 采用 segmentio/kafka-go 实现, 支持按 broker 列表初始化、按 topic/group 消费.
// 设计约定: 业务服务之间事件驱动通过 Kafka 解耦(停车地磁事件、告警联动、公告推送等).
package kafka

// 主题常量: 统一命名, 避免散落硬编码.
const (
	TopicDeviceTelemetry = "device-telemetry" // M1 设备遥测(地磁/门禁上报)
	TopicParkingEntry    = "parking-entry"    // 车辆入场事件(parking-service 发布)
	TopicParkingExit     = "parking-exit"     // 车辆离场事件(parking-service 发布)
	TopicAlarm           = "alarm-event"      // 告警事件(M3 安防消费)
	TopicNotice          = "notice-event"     // 公告发布事件(通知类消费)
	TopicWorkorder       = "workorder-event"  // 工单状态事件(workorder-service 发布, M5/通知类消费)
)

// 消费者组常量(同一服务多实例共享消费位移).
const (
	GroupParking   = "parking-service"
	GroupAlarm     = "alarm-service"
	GroupNotice    = "notice-service"
	GroupWorkorder = "workorder-service"
	GroupDevice    = "device-service" // M1 遥测回写(设备状态/影子/时序库)
)
