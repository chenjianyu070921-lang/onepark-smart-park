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
	// MySQLDSN 设备档案查询(充入 tenant_id/zone_id)用的只读连接;
	// 未配置时的行为由 ArchiveRequired 决定.
	MySQLDSN string `json:",env=MYSQL_DSN,optional"`
	// ArchiveRequired 是否强制要求设备档案库可用:
	// true(默认) 且 MySQLDSN 缺失 → 启动失败(fail-closed), 防止 tenant_id=0 消息污染下游告警/计费;
	// false → 零值放行降级(仅本地调试用), 启动时打 WARN 日志.
	ArchiveRequired bool `json:",env=ARCHIVE_REQUIRED,default=true"`
}
