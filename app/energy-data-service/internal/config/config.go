package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"

	"onepark/common/kafka"
)

type Config struct {
	rest.RestConf

	// RPC 对外提供的 gRPC 服务配置(接口54 给 M5 大屏)
	RPC zrpc.RpcServerConf

	// MySQL 组内公用库 onepark-smart-park
	MySQL struct {
		DataSource string
	}

	// Redis 缓存: 实时能耗、日报
	Redis redis.RedisConf

	// Kafka 消费 M1 遥测数据, 写入 energy_reading
	Kafka struct {
		Brokers []string
		// Topic 一般留空, 让代码用 common/kafka 的公共常量(见 TopicName),
		// 免得 M1 发到一个名字、M4 订阅另一个名字, 结果一条都收不到
		Topic string `json:",optional"`
		Group string
	}

	// DefaultZone 设备查不到归属区域时归到哪, 默认"未分配"
	// 兜底而不是丢弃: 数据不丢, 运维看到日报里"未分配"有量就知道要去补归属
	DefaultZone string `json:",default=未分配"`
}

// TopicName 实际订阅的 topic
// 配置里没写就用 common 包的公共常量, 让 topic 只有"一个真相来源",
// M1 哪天改了常量, 这边跟着变, 不会再出现两边对不上还互相不知道的情况
func (c *Config) TopicName() string {
	if c.Kafka.Topic != "" {
		return c.Kafka.Topic
	}
	return kafka.TopicDeviceTelemetry
}
