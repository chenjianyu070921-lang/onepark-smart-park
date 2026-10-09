package svc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	energypb "onepark/proto/energy"

	"onepark/app/dashboard-service/internal/config"
)

type ServiceContext struct {
	Config config.Config

	// Energy M4 能耗服务客户端(接口54: GetDailyReport, 给大屏能耗卡片出数)
	Energy energypb.EnergyDataServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	// 服务间不经过 etcd, 直接按配置的地址连(内网固定部署)
	conn, err := grpc.NewClient(c.EnergyDataRpc.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic("连接 energy-data-service 失败(" + c.EnergyDataRpc.Endpoint + "): " + err.Error())
	}

	return &ServiceContext{
		Config: c,
		Energy: energypb.NewEnergyDataServiceClient(conn),
	}
}
