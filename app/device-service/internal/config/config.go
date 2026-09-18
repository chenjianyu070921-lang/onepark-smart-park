package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"

	"onepark/common/mqtt"
	"onepark/common/tdengine"
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

	// MQTT 指令下行通道; Broker 为空时降级为仅落库(不阻断指令受理)
	MQTT mqtt.Conf

	// TDengine 遥测时序落库; Endpoint 为空时跳过写时序库
	TDengine tdengine.Conf

	// JwtSecret HTTP 接口鉴权密钥; 为空时放行(M6 认证服务就绪前保持可联调)
	JwtSecret string `json:",env=JWT_SECRET,optional"`

	// CommandTimeoutSec 指令超时时间(秒), 超时后由定时任务置为超时状态
	CommandTimeoutSec int `json:",default=30"`
	// TimeoutScanIntervalSec 超时扫描周期(秒)
	TimeoutScanIntervalSec int `json:",default=15"`
	// TimeoutScanBatchLimit 单次扫描的批量上限, 避免一轮处理过多指令
	TimeoutScanBatchLimit int `json:",default=200"`
	// CommandMaxResend 超时指令的最大重发次数(不含首发); 0 表示不重发, 超时直接置失败
	CommandMaxResend int `json:",default=2"`
}
