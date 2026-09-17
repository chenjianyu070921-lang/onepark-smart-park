package svc

import (
	"log"

	"onepark/app/access-control-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/zrpc"
)

// ServiceContext 持有 access-control-service 运行时的全局依赖.
// P0 门禁基础逻辑: MySQL(门禁点位/通行记录) + M1 device gRPC(远程开门 SendCommand) + Redis(预留).
type ServiceContext struct {
	Config    config.Config
	DB        *gormx.DB                    // GORM MySQL 连接; 未配置 DSN 时为 nil(接口返回明确错误, 不 panic)
	Redis     *redisx.Client               // Redis 客户端(门禁权限缓存, 预留)
	DeviceRPC devicepb.DeviceServiceClient // M1 device gRPC(远程开门); 未配置时为 nil, 开门返回明确错误
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
		log.Printf("[warn] access-control-service mysql data source is empty, db not initialized")
	}

	// M1 设备 gRPC 客户端: 未配置 Endpoints/Target/Etcd 时为 nil, 远程开门返回明确错误.
	var deviceCli devicepb.DeviceServiceClient
	if len(c.DeviceRPC.Endpoints) > 0 || c.DeviceRPC.Target != "" || len(c.DeviceRPC.Etcd.Hosts) > 0 {
		deviceCli = devicepb.NewDeviceServiceClient(zrpc.MustNewClient(c.DeviceRPC).Conn())
	} else {
		log.Printf("[warn] access-control-service device rpc not configured, remote open disabled")
	}

	return &ServiceContext{
		Config:    c,
		DB:        db,
		Redis:     redisx.NewClient(&c.Redis),
		DeviceRPC: deviceCli,
	}
}
