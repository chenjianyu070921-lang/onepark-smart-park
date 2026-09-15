package svc

import (
	"log"

	"onepark/app/workorder-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
)

// ServiceContext 持有 workorder-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接、Redis 客户端与 Kafka 生产者, 供 logic 层使用.
type ServiceContext struct {
	Config   config.Config
	DB       *gormx.DB       // GORM MySQL 连接
	Redis    *redisx.Client  // Redis 客户端
	Producer *kafka.Producer // Kafka 生产者(发布工单状态事件 workorder-event)
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
		log.Printf("[warn] workorder-service mysql data source is empty, db not initialized")
	}

	// Kafka 生产者: 未配置 broker 时为 nil, logic 层发事件前判空跳过.
	var producer *kafka.Producer
	if c.Kafka.Brokers != "" {
		producer = kafka.NewProducer(c.Kafka.Brokers)
	} else {
		log.Printf("[warn] workorder-service kafka brokers empty, producer not initialized")
	}

	return &ServiceContext{
		Config:   c,
		DB:       db,
		Redis:    redisx.NewClient(&c.Redis),
		Producer: producer,
	}
}
