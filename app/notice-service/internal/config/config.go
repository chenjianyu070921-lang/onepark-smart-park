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
	Kafka KafkaConf        // Kafka 连接配置
}

// KafkaConf 同时服务两个方向:
//   - 生产: 公告发布时投递 notice-event(Brokers);
//   - 消费: 消费 workorder-event 生成站内通知(Topic/Group/Enabled).
//
// ⚠️ 共享 broker 纪律(同 workorder-service): Group 必须带项目+服务+环境;
// Enabled 默认 false, 消费者会真实写库(notice/notice_read), 防止误开产生脏数据.
type KafkaConf struct {
	Brokers string `json:",optional"`                  // 多个 broker 用逗号分隔, 如 kafka1:9092,kafka2:9092
	Topic   string `json:",default=workorder-event"`   // 消费的工单事件主题(M2 workorder 发布)
	Group   string `json:",default=m2-notice-dev"`     // 消费组(项目-服务-环境, 严禁与他人重名)
	Enabled bool   `json:",default=false"`             // 站内通知消费者开关, 默认关闭
}
