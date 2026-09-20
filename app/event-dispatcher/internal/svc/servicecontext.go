package svc

import (
	"fmt"
	"os"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/event-dispatcher/internal/archive"
	"onepark/app/event-dispatcher/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

type ServiceContext struct {
	Config   config.Config
	Producer *kafka.Producer
	Resolver *archive.Resolver
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

	// 设备档案只读连接: 未配置 DSN 时保持 nil, dispatch 侧零值放行(降级启动约定).
	var resolver *archive.Resolver
	if dsn := os.ExpandEnv(c.MySQLDSN); dsn != "" {
		db, err := gormx.NewDB(dsn)
		if err != nil {
			logx.Must(fmt.Errorf("初始化 MySQL 失败: %w", err))
		}
		resolver = archive.NewResolver(archive.NewGormReader(db), 30*time.Second)
	}

	return &ServiceContext{
		Config:   c,
		Producer: kafka.NewProducer(c.KafkaBrokers),
		Resolver: resolver,
	}
}
