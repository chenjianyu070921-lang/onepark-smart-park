package config

import (
	"github.com/zeromicro/go-zero/rest"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 visitor-service 的运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis、Kafka 等连接信息.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // MySQL 连接配置
	Redis redisx.RedisConf // Redis 连接配置
	Kafka KafkaConf        // Kafka 连接配置
}

// KafkaConf 定义 Kafka broker 列表, 供后续访客事件投递使用.
type KafkaConf struct {
	Brokers string // 多个 broker 用逗号分隔, 如 kafka1:9092,kafka2:9092
}
