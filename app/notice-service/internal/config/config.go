package config

import (
	"github.com/zeromicro/go-zero/rest"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 notice-service 的运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis、Kafka 等连接信息.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // MySQL 连接配置
	Redis redisx.RedisConf // Redis 连接配置
	Kafka KafkaConf        // Kafka 连接配置(生产 notice-event / 消费 workorder-event)
	// NoticeEvent notice-event 消费配置(公告发布事件 → Redis PubSub 推送在线用户).
	// 独立配置段: 与 workorder-event 消费各自开关/独立消费组, 互不影响.
	NoticeEvent KafkaConf `json:",optional"`
	// PublishCron 定时发布调度配置(P1): 到达 publish_at 的草稿公告自动发布并投递 notice-event.
	// Enabled 默认关闭, 防止误开在共享环境产生真实推送副作用.
	PublishCron PublishCronConf `json:",optional"`
}

// PublishCronConf 定义定时发布调度参数.
type PublishCronConf struct {
	Enabled bool   `json:",default=false"` // 调度开关, 默认关闭(部署时显式开启)
	Spec    string `json:",optional"`      // 扫描周期(robfig cron 5 段表达式); 空值时代码内回落为每分钟
}

// KafkaConf 同时服务两个方向:
//   - 生产: 公告发布时投递 notice-event(Brokers);
//   - 消费: 消费 workorder-event 生成站内通知(Topic/Group/Enabled);
//     或(NoticeEvent 段)消费 notice-event 做 PubSub 在线推送.
//
// ⚠️ 共享 broker 纪律(同 workorder-service): Group 必须带项目+服务+环境;
// Enabled 默认 false, 消费者会真实产生副作用(写库/推送), 防止误开产生脏数据.
type KafkaConf struct {
	Brokers string `json:",optional"`                // 多个 broker 用逗号分隔, 如 kafka1:9092,kafka2:9092
	Topic   string `json:",default=workorder-event"` // 消费的工单事件主题(M2 workorder 发布)
	Group   string `json:",default=m2-notice-dev"`   // 消费组(项目-服务-环境, 严禁与他人重名)
	Enabled bool   `json:",default=false"`           // 消费者开关, 默认关闭
}
