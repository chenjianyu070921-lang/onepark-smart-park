package svc

import (
	"log"

	"onepark/app/dashboard-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
	workorderpb "onepark/proto/workorder"
	"github.com/zeromicro/go-zero/zrpc"
)

// ServiceContext 持有 dashboard-service 运行时的全局依赖.
type ServiceContext struct {
	Config       config.Config
	DB           *gormx.DB                         // GORM MySQL 连接(可选)
	Redis        *redisx.Client                    // Redis 客户端(概览缓存)
	WorkorderRPC workorderpb.WorkorderServiceClient // workorder gRPC 客户端(M2 工单数据源)
}

// NewServiceContext 根据配置初始化全局依赖.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] dashboard mysql data source empty, db not initialized")
	}

	redis := redisx.NewClient(&c.Redis)

	// workorder gRPC 客户端: 通过 Endpoints 直连(无注册中心);
	// RpcClientConf.NonBlock 默认 true, 允许在 gRPC 未就绪时启动, 由 logic 层降级处理.
	woClient := workorderpb.NewWorkorderServiceClient(
		zrpc.MustNewClient(c.WorkorderRPC).Conn(),
	)

	return &ServiceContext{
		Config:       c,
		DB:           db,
		Redis:        redis,
		WorkorderRPC: woClient,
	}
}
