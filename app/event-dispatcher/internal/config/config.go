package config

type Config struct {
	Name          string
	EMQXBroker    string `json:",env=EMQX_BROKER,default=tcp://emqx:1883"`
	EMQXClientId  string `json:",env=EMQX_CLIENT,default=event-dispatcher"`
	KafkaBrokers  string `json:",env=KAFKA_BROKERS,default=kafka:9092"`
}
