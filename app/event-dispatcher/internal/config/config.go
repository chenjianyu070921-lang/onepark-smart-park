package config

type Config struct {
	Name         string
	EMQXBroker   string `json:",env=EMQX_BROKER,default=tcp://emqx:1883"`
	EMQXClientId string `json:",env=EMQX_CLIENT,default=event-dispatcher"`
	EMQXUsername string `json:",env=EMQX_USERNAME,optional"`
	EMQXPassword string `json:",env=EMQX_PASSWORD,optional"`
	// EMQXTopic 订阅主题, 设备侧上报约定: onepark/device/{productKey}/{deviceId}/{kind}
	// kind 取值: event(事件) / telemetry(遥测) / status(上下线)
	EMQXTopic    string `json:",env=EMQX_TOPIC,default=onepark/device/#"`
	KafkaBrokers string `json:",env=KAFKA_BROKERS,default=kafka:9092"`
}
