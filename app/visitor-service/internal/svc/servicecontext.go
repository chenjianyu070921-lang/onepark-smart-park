package svc

import (
	"log"

	"onepark/app/visitor-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/zrpc"
)

// ServiceContext 持有 visitor-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接、Redis 客户端、Kafka 生产者与 M1 设备 gRPC 客户端(访客开门联动).
type ServiceContext struct {
	Config    config.Config
	DB        *gormx.DB                    // GORM MySQL 连接
	Redis     *redisx.Client               // Redis 客户端
	Producer  *kafka.Producer              // Kafka 生产者(发布访客事件 visitor-event, 供大屏等消费); 未配置时为 nil
	DeviceRPC devicepb.DeviceServiceClient // M1 device gRPC(签入开门); 未配置时为 nil
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
		log.Printf("[warn] visitor-service mysql data source is empty, db not initialized")
	}

	// M1 设备 gRPC 客户端: 未配置 Endpoints/Target/Etcd 时为 nil, 签入开门降级跳过.
	var deviceCli devicepb.DeviceServiceClient
	if len(c.DeviceRPC.Endpoints) > 0 || c.DeviceRPC.Target != "" || len(c.DeviceRPC.Etcd.Hosts) > 0 {
		deviceCli = devicepb.NewDeviceServiceClient(zrpc.MustNewClient(c.DeviceRPC).Conn())
	} else {
		log.Printf("[warn] visitor-service device rpc not configured, M1 door open disabled")
	}

	// Kafka 生产者: 未配置 Brokers 时为 nil, 访客事件投递降级跳过(不影响主流程).
	var producer *kafka.Producer
	if c.Kafka.Brokers != "" {
		producer = kafka.NewProducer(c.Kafka.Brokers)
	} else {
		log.Printf("[warn] visitor-service kafka brokers empty, visitor-event producer not initialized")
	}

	return &ServiceContext{
		Config:    c,
		DB:        db,
		Redis:     redisx.NewClient(&c.Redis),
		Producer:  producer,
		DeviceRPC: deviceCli,
	}
}
