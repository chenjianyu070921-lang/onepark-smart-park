// Package kafka 定义全项目共用的 Kafka topic 名字。
//
// 为什么要单独放一个包: topic 名是"生产者"和"消费者"之间的契约, 两边各写一份
// 字符串迟早对不上(比如一边 device-telemetry 一边 onepark.device.telemetry,
// 结果消费者一条都收不到, 而且没有任何报错)。统一从这里取, 改一处全项目生效。
package kafka

// 以下是已定义的 topic, 新增 topic 请在这里加常量并在注释里写清"谁发、谁收"。

// TopicDeviceTelemetry 设备遥测数据
// 生产者: M1 device-service / event-dispatcher
// 消费者: M4 energy-data-service(接口55, 写 energy_reading)
// 消息体格式见 docs/m4/M4与M1对接说明.md
const TopicDeviceTelemetry = "device-telemetry"
