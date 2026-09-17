package config

import (
	"onepark/common/gormx"
	"onepark/common/redisx"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

// Config 服务配置 (M3 首个落地者, 见 KI-4).
// P0 门禁基础逻辑接入 MySQL(GORM) 与 M1 device-service gRPC(远程开门);
// Kafka/ES/Nacos 仍为可选占位, 未配置不影响启动.
type Config struct {
	rest.RestConf
	MySQL     gormx.MySQLConf    `json:",optional"` // GORM 数据源(门禁点位/通行记录)
	Redis     redisx.RedisConf   `json:",optional"` // Redis(门禁权限缓存, 预留)
	DeviceRPC zrpc.RpcClientConf `json:",optional"` // M1 device-service gRPC(远程开门 SendCommand)
	Kafka     KafkaConf          `json:",optional"` // Kafka 生产/消费配置 (Day6 接入)
	ES        ESConf             `json:",optional"` // Elasticsearch 配置 (Day13 接入)
	Nacos     NacosConf          `json:",optional"` // 注册/配置中心 (可选)
}

// KafkaConf Kafka 生产/消费配置 (Day6 接入).
type KafkaConf struct {
	Brokers []string `json:",default=[]"`
	GroupID string   `json:",default=access-control-service-group"`
}

// ESConf Elasticsearch 配置 (Day13 接入).
type ESConf struct {
	Addresses []string `json:",default=[]"`
	Username  string   `json:",optional"`
	Password  string   `json:",optional"`
}

// NacosConf 注册/配置中心 (可选).
type NacosConf struct {
	Address string `json:",default="`
}
