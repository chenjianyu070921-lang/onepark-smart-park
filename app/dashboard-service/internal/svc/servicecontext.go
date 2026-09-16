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
	alarmpb "onepark/proto/alarm"
	energypb "onepark/proto/energy"
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
//
// 状态(2026-09-16): M2 工单 / M3 告警 / M4 能耗 三个契约均已就绪 -> 真实 gRPC 客户端;
// M1 仍无设备统计接口 -> 保持显式降级占位。
func newProviders(c config.Config) Providers {
	providers := Providers{
		// M1 无设备统计 gRPC(只有 Ping/SendCommand/GetDevice), 且规范禁止服务间走 HTTP
		Device: provider.Device{},
	}

	providers.WorkOrder, providers.Alarm, providers.Energy = wireWorkOrder(c), wireAlarm(c), wireEnergy(c)
	return providers
}

// configured 判断某路 gRPC 客户端配置是否可用(Endpoints / Target / Etcd 任一非空即可用).
func configured(conf zrpc.RpcClientConf) bool {
	return conf.Target != "" || len(conf.Endpoints) > 0 || len(conf.Etcd.Hosts) > 0
}

// wireWorkOrder 装配 M2 工单数据源.
func wireWorkOrder(c config.Config) provider.WorkOrderProvider {
	if !configured(c.Workorder) {
		log.Printf("[warn] dashboard workorder grpc config is empty, work order card will degrade")
		return provider.WorkOrderNotReady{}
	}
	conn := zrpc.MustNewClient(c.Workorder).Conn()
	log.Printf("[dashboard] workorder grpc client initialized")
	return provider.NewWorkOrder(workorderpb.NewWorkorderServiceClient(conn))
}

// wireAlarm 装配 M3 告警数据源.
func wireAlarm(c config.Config) provider.AlarmProvider {
	if !configured(c.Alarm) {
		log.Printf("[warn] dashboard alarm grpc config is empty, alarm card will degrade")
		return provider.AlarmNotReady{}
	}
	conn := zrpc.MustNewClient(c.Alarm).Conn()
	log.Printf("[dashboard] alarm grpc client initialized")
	return provider.NewAlarm(alarmpb.NewAlarmServiceClient(conn))
}

// wireEnergy 装配 M4 能耗数据源.
func wireEnergy(c config.Config) provider.EnergyProvider {
	if !configured(c.Energy) {
		log.Printf("[warn] dashboard energy grpc config is empty, energy card will degrade")
		return provider.EnergyNotReady{}
	}
	conn := zrpc.MustNewClient(c.Energy).Conn()
	log.Printf("[dashboard] energy grpc client initialized")
	return provider.NewEnergy(energypb.NewEnergyDataServiceClient(conn))
}
