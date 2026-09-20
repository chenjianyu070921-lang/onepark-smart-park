package main

import (
	"flag"
	"fmt"

	"onepark/app/energy-data-service/internal/config"
	"onepark/app/energy-data-service/internal/handler"
	"onepark/app/energy-data-service/internal/mq"
	"onepark/app/energy-data-service/internal/server"
	"onepark/app/energy-data-service/internal/svc"
	energypb "onepark/proto/energy"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/energydata-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// conf.UseEnv() 必填: 本服务既未用 conf.UseEnv() 也未在 ServiceContext 里 os.ExpandEnv,
	// 不启用则 etc/energydata-api.yaml 的 ${ENERGY_DATA_MYSQL_DSN}/${REDIS_ADDR}/... 全部以字面量生效.
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	ctx := svc.NewServiceContext(c)

	// HTTP 服务: 接口52/53, 给页面和 ApiPost 调用
	httpServer := rest.MustNewServer(c.RestConf)
	httpServer.Use(middleware.IdentityFromHeader)
	defer httpServer.Stop()
	handler.RegisterHandlers(httpServer, ctx)

	// gRPC 服务: 接口54, 给 M5 dashboard-service 大屏调用
	rpcServer := zrpc.MustNewServer(c.RPC, func(grpcServer *grpc.Server) {
		energypb.RegisterEnergyDataServiceServer(grpcServer, server.NewEnergyDataServer(ctx))
		if c.RPC.Mode == service.DevMode || c.RPC.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer rpcServer.Stop()

	// Kafka 消费者: 接口55, 收 M1 上报的遥测数据写进 energy_reading
	consumer := mq.NewConsumer(c.Kafka.Brokers, c.Kafka.Topic, c.Kafka.Group, ctx.EnergyReading)

	// 三个一起启动(HTTP + gRPC + Kafka消费者), 缺一个就整体退出
	group := service.NewServiceGroup()
	group.Add(httpServer)
	group.Add(rpcServer)
	group.Add(consumer)

	fmt.Printf("Starting http at %s:%d, rpc at %s, kafka topic=%s...\n",
		c.Host, c.Port, c.RPC.ListenOn, c.Kafka.Topic)
	group.Start()
}
