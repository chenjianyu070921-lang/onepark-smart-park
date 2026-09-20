package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
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
		Topic   string
		Group   string
	}
}
