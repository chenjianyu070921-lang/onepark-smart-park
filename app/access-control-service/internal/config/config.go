package config

import (
	"onepark/common/gormx"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

// Config 定义 access-control-service 的运行配置.
// Day2 各中间件均为可选: 未接入前 yaml 不配置对应段落也能启动(见 P0-6 三服务同时启动验收).
type Config struct {
	rest.RestConf
	Redis      redis.RedisConf    `json:",optional"`
	MySQL      gormx.MySQLConf    `json:",optional"` // access_db 连接配置(审计留痕)
	DeviceRPC  zrpc.RpcClientConf `json:",optional"` // M1 device-service gRPC(远程开门下发命令)
	RemoteOpen RemoteOpenConf     `json:",optional"` // 远程开门权限兜底(M6 鉴权就绪前的 interim 方案)
	Kafka      KafkaConf          `json:",optional"`
	ES         ESConf             `json:",optional"`
	Nacos      NacosConf          `json:",optional"`
}

// RemoteOpenConf 远程开门的设备白名单.
// M6 CheckPermission 尚未提供 gRPC 契约(proto 中不存在该 RPC), 先用设备粒度白名单兜底,
// 避免 8010 在内网被直连调用即可开任意门; M6 上线后本段由权限校验替代.
// AllowedDeviceIds 为空表示拒绝一切远程开门(fail-closed).
type RemoteOpenConf struct {
	AllowedDeviceIds []string `json:",default=[]"`
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
