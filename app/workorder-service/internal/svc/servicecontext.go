package svc

import (
	"context"
	"log"

	"onepark/app/workorder-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/minio"
	"onepark/common/redisx"
)

// ServiceContext 持有 workorder-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接、Redis 客户端与 Kafka 生产者, 供 logic 层使用.
type ServiceContext struct {
	Config   config.Config
	DB       *gormx.DB       // GORM MySQL 连接
	Redis    *redisx.Client  // Redis 客户端
	Producer *kafka.Producer // Kafka 生产者(发布工单状态事件 workorder-event)
	MinIO    *miniox.Client  // MinIO 对象存储客户端(工单附件)
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

	// MinIO 客户端: 未配置 Endpoint 时为 nil, 附件上传接口会返回"对象存储未配置".
	var minioClient *miniox.Client
	if c.MinIO.Endpoint != "" {
		mc, err := miniox.NewClient(c.MinIO)
		if err != nil {
			log.Fatalf("init minio failed: %v", err)
		}
		if err := miniox.EnsureBucket(context.Background(), mc, c.MinIO.Bucket); err != nil {
			log.Printf("[warn] ensure minio bucket %q failed: %v", c.MinIO.Bucket, err)
		}
		minioClient = mc
	} else {
		log.Printf("[warn] workorder-service minio endpoint empty, attachment upload disabled")
	}

	return &ServiceContext{
		Config:   c,
		DB:       db,
		Redis:    redisx.NewClient(&c.Redis),
		Producer: producer,
		MinIO:    minioClient,
	}
}
