package svc

import (
	"os"

	"onepark/app/event-dispatcher/internal/config"
	"onepark/common/kafka"
)

type ServiceContext struct {
	Config   config.Config
	Producer *kafka.Producer
}

// NewServiceContext 初始化上下文.
// 约定: yaml 中只写 ${XXX} 占位, 此处统一展开环境变量并填充默认值,
// 避免 conf.MustLoad 未开启 env 时把占位符当成真实地址连接.
func NewServiceContext(c config.Config) *ServiceContext {
	c.EMQXBroker = os.ExpandEnv(c.EMQXBroker)
	if c.EMQXBroker == "" {
		c.EMQXBroker = "tcp://localhost:1883"
	}
	c.EMQXClientId = os.ExpandEnv(c.EMQXClientId)
	if c.EMQXClientId == "" {
		c.EMQXClientId = "event-dispatcher"
	}
	c.EMQXUsername = os.ExpandEnv(c.EMQXUsername)
	c.EMQXPassword = os.ExpandEnv(c.EMQXPassword)
	c.EMQXTopic = os.ExpandEnv(c.EMQXTopic)
	if c.EMQXTopic == "" {
		c.EMQXTopic = "onepark/device/#"
	}
	c.KafkaBrokers = os.ExpandEnv(c.KafkaBrokers)
	if c.KafkaBrokers == "" {
		c.KafkaBrokers = "localhost:9092"
	}

	return &ServiceContext{
		Config:   c,
		Producer: kafka.NewProducer(c.KafkaBrokers),
	}
}
