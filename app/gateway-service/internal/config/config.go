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

	// CoAP 多协议接入(与 TCP 共用 frame.Session 帧语义, 见 design/coap-integration-plan.md).
	// Enabled 默认关: 灰度可控, 回滚 = 关配置, 零 DB 依赖零回归.
	CoAP struct {
		Enabled     bool   `json:",env=COAP_ENABLED,default=false"`
		Host        string `json:",env=COAP_HOST,default=0.0.0.0"`
		Port        int    `json:",env=COAP_PORT,default=5683"` // NoSec UDP 端口(内网/联调用)
		DTLSEnabled bool   `json:",env=COAP_DTLS_ENABLED,default=false"`
		DTLSPort    int    `json:",env=COAP_DTLS_PORT,default=5684"` // DTLS 端口(生产强制加密)
		// DTLSPSK 单部署共享预共享密钥: DTLS 通道加密用. 注意 pion DTLS PSK 服务端为单一共享密钥,
		// 无法把 DTLS 身份绑定到具体设备, 故设备仍走 frame 既有 bcrypt 逐设备认证(见 coap/server.go).
		DTLSPSK string `json:",env=COAP_DTLS_PSK"`
	}
}
