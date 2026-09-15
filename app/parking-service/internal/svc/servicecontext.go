package svc

import (
	"context"
	"log"

	"onepark/app/parking-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
)

// ServiceContext 持有 parking-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接、Redis 客户端与 Kafka 生产者, 供 logic 层使用.
type ServiceContext struct {
	Config   config.Config
	DB       *gormx.DB       // GORM MySQL 连接
	Redis    *redisx.Client  // Redis 客户端
	Producer *kafka.Producer // Kafka 生产者(发布停车/告警事件)
}

// NewServiceContext 根据配置初始化全局依赖.
// 若配置了 MySQL DSN 但初始化失败, 直接退出进程, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] parking-service mysql data source is empty, db not initialized")
	}

	var producer *kafka.Producer
	if c.Kafka.Brokers != "" {
		producer = kafka.NewProducer(c.Kafka.Brokers)
	} else {
		log.Printf("[warn] parking-service kafka brokers empty, producer not initialized")
	}

	return &ServiceContext{
		Config:   c,
		DB:       db,
		Redis:    redisx.NewClient(&c.Redis),
		Producer: producer,
	}
}

// StartConsumers 启动后台 Kafka 消费者(地磁遥测 -> 停车记录), 独立 goroutine 运行.
// 仅在配置了 Kafka 时启动; 消费失败不影响主服务.
func (s *ServiceContext) StartConsumers(ctx context.Context) {
	if s.Config.Kafka.Brokers == "" {
		log.Printf("[warn] parking-service kafka brokers empty, skip consumers")
		return
	}
	go func() {
		consumer := kafka.NewConsumer(s.Config.Kafka.Brokers, kafka.TopicDeviceTelemetry, kafka.GroupParking)
		defer consumer.Close()
		log.Printf("[info] parking-service start consume topic=%s", kafka.TopicDeviceTelemetry)
		if err := consumer.Consume(ctx, s.handleTelemetry); err != nil && ctx.Err() == nil {
			log.Printf("[error] parking telemetry consumer exited: %v", err)
		}
	}()
}
