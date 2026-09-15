package config

import (
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 visitor-service 的运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis、Kafka 及 M1 设备 gRPC(访客开门联动).
type Config struct {
	rest.RestConf
	MySQL     gormx.MySQLConf    // MySQL 连接配置
	Redis     redisx.RedisConf   // Redis 连接配置
	Kafka     KafkaConf          // Kafka 连接配置
	DeviceRPC zrpc.RpcClientConf // M1 device-service gRPC(调用 SendCommand 开门)
	Door      DoorConf           // 门岗开门设备配置
}

// KafkaConf 定义 Kafka broker 列表, 供后续访客事件投递使用.
type KafkaConf struct {
	Brokers string // 多个 broker 用逗号分隔, 如 kafka1:9092,kafka2:9092
}

// DoorConf 定义访客签入开门所下发的门岗设备.
type DoorConf struct {
	DeviceID string `json:",default="`          // 门岗入口设备ID(M1 设备中心注册的 device_id)
	Command  string `json:",default=open_door"` // 开门指令名, 默认 open_door
}
