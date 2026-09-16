package config

import (
	"github.com/zeromicro/go-zero/rest"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 alarm-service 的运行配置(与 M2 四服务保持同构).
// MySQL/Redis/Kafka 在 ServiceContext 中初始化, 未配置时降级为空实现并以 WARN 提示, 不阻断启动.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // alarm_db 连接配置
	Redis redisx.RedisConf // Redis 配置(幂等去重 / 滑动窗口)
	Kafka KafkaConf        // Kafka 配置(消费 M1 设备遥测生成告警)
	ES    ESConf           // Elasticsearch 配置(Day13 接入, 暂未使用)
	Nacos NacosConf        // 注册/配置中心(可选)
}

// KafkaConf Kafka 消费配置.
// Brokers 为逗号分隔字符串, 与 common/kafka.NewConsumer 签名一致.
type KafkaConf struct {
	Brokers string `json:",optional"`
	GroupID string `json:",default=alarm-service"`
}

// ESConf Elasticsearch 配置 (Day13 接入).
type ESConf struct {
	Addresses []string `json:",optional"`
	Username  string   `json:",optional"`
	Password  string   `json:",optional"`
}

// NacosConf 注册/配置中心 (可选).
type NacosConf struct {
	Address string `json:",optional"`
}
