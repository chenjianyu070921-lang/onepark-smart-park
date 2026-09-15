package svc

import (
	"context"
	"log"
	"time"

	"github.com/zeromicro/go-zero/zrpc"

	"onepark/app/dashboard-service/internal/config"
	"onepark/app/dashboard-service/internal/provider"
	"onepark/common/gormx"
	"onepark/common/redisx"
	workorderpb "onepark/proto/workorder"
)

// Providers 汇总 dashboard 聚合所需的全部数据源端口.
type Providers struct {
	WorkOrder provider.WorkOrderProvider
	Alarm     provider.AlarmProvider
	Device    provider.DeviceProvider
	Energy    provider.EnergyProvider
}

// ServiceContext 持有 dashboard-service 运行时的全局依赖.
type ServiceContext struct {
	Config    config.Config
	DB        *gormx.DB      // GORM MySQL 连接(dashboard_db)
	Redis     *redisx.Client // Redis 客户端(聚合缓存, key 带租户维度)
	Providers Providers      // 聚合数据源(依赖倒置, 便于降级与替换)
}

// NewServiceContext 根据配置初始化全局依赖.
// 初始化后主动探活一次, 使启动日志能明确反映 MySQL / Redis 连接状态.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		opened, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("[dashboard] init mysql failed: %v", err)
		}
		sqlDB, err := opened.DB()
		if err != nil {
			log.Fatalf("[dashboard] get sql.DB failed: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			log.Fatalf("[dashboard] mysql ping failed: %v", err)
		}
		log.Printf("[dashboard] mysql connected ok")
		db = opened
	} else {
		log.Printf("[warn] dashboard-service mysql data source is empty, db not initialized")
	}

	rdb := redisx.NewClient(&c.Redis)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("[dashboard] redis ping failed: %v", err)
	}
	log.Printf("[dashboard] redis connected ok")

	return &ServiceContext{
		Config:    c,
		DB:        db,
		Redis:     rdb,
		Providers: newProviders(c),
	}
}

// newProviders 装配各数据源端口.
// M2 工单已就绪 -> 真实 gRPC 客户端; M1/M3/M4 的契约尚未定义 -> 显式降级占位.
func newProviders(c config.Config) Providers {
	providers := Providers{
		Alarm:  provider.Alarm{},
		Device: provider.Device{},
		Energy: provider.Energy{},
	}

	if c.Workorder.Target != "" || len(c.Workorder.Endpoints) > 0 || len(c.Workorder.Etcd.Hosts) > 0 {
		client := zrpc.MustNewClient(c.Workorder)
		providers.WorkOrder = provider.NewWorkOrder(workorderpb.NewWorkorderServiceClient(client.Conn()))
		log.Printf("[dashboard] workorder grpc client initialized")
	} else {
		providers.WorkOrder = provider.WorkOrderNotReady{}
		log.Printf("[warn] dashboard workorder grpc config is empty, work order card will degrade")
	}

	return providers
}
