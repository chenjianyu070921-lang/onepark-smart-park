package config

// Config 多协议网关配置.
// 当前实现 TCP 长连接接入(按行分隔的 JSON 帧), CoAP 后续引入 plgd-dev/go-coap.
type Config struct {
	Host string `json:",env=HOST,default=0.0.0.0"`
	Port int    `json:",env=PORT,default=7000"`

	MySQL struct {
		// DataSource 支持 ${MYSQL_DSN} 环境变量占位
		DataSource   string
		MaxOpenConns int `json:",default=20"`
		MaxIdleConns int `json:",default=10"`
	}

	KafkaBrokers string `json:",env=KAFKA_BROKERS,default=localhost:9092"`

	// MaxFrameBytes 单帧最大长度, 超出即断开, 防止恶意长帧打爆内存
	MaxFrameBytes int `json:",default=8192"`
	// ReadTimeoutSec 连接读超时(空闲断开), 设备需按心跳保活
	ReadTimeoutSec int `json:",default=120"`
	// AuthTimeoutSec 建连后完成认证的时限, 超时断开
	AuthTimeoutSec int `json:",default=10"`
}
