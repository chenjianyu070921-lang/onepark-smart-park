package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	rest.RestConf
	// Rpc 命名嵌入, yaml 中为独立 Rpc 段, 避免与 RestConf 的 Timeout 等字段冲突
	Rpc   zrpc.RpcServerConf
	MySQL struct {
		// DataSource 支持 ${MYSQL_DSN} 环境变量占位, 由 ServiceContext 做 os.ExpandEnv
		DataSource   string
		MaxOpenConns int `json:",default=20"`
		MaxIdleConns int `json:",default=10"`
	}
	Redis redis.RedisConf

	// KafkaBrokers 逗号分隔的 broker 列表, 用于设备事件投递
	KafkaBrokers string `json:",env=KAFKA_BROKERS,default=localhost:9092"`
}
