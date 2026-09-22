package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"onepark/app/workorder-service/internal/config"
	"onepark/app/workorder-service/internal/consumer"
	"onepark/app/workorder-service/internal/cron"
	"onepark/app/workorder-service/internal/handler"
	"onepark/app/workorder-service/internal/rpcserver"
	"onepark/app/workorder-service/internal/svc"
	"onepark/common/health"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	workorderpb "onepark/proto/workorder"
)

var configFile = flag.String("f", "etc/workorder-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传 + RBAC 上下文(网关注入 x-tenant-id/x-user-id/x-role-ids).
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)
	server.Use(cmw.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 工单 gRPC 服务(M5 运营大屏 ListWorkOrders 聚合查询): 与 REST 同进程双模监听 9091.
	// 注册 WorkorderServer 实现(rpcserver 包), 由 M5 dashboard 经 WORKORDER_RPC_ENDPOINTS 调用.
	grpcServer := zrpc.MustNewServer(c.Rpc, func(s *grpc.Server) {
		workorderpb.RegisterWorkorderServiceServer(s, rpcserver.NewWorkorderServer(ctx.DB))
	})
	defer grpcServer.Stop()
	go func() {
		grpcServer.Start()
	}()

	// 健康检查: /api/healthz 存活(不探依赖), /api/readyz 就绪(探 MySQL + Redis; Kafka 由 Producer 旁路兜底).
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
		{Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(ctx.DB, ctx.Redis)},
	})

	// 告警自动建单消费者: 消费 alarm-event → 幂等建报修工单(alarm_id 唯一键去重).
	// Kafka.Enabled=false 时 Start 内部直接跳过; 注意 Consumer.Consume 阻塞运行,
	// 必须放在独立 goroutine 中, 否则会阻塞主线程导致 HTTP 服务起不来.
	go consumer.NewAlarmRunner(c.Kafka, ctx.DB, ctx.Producer).Start(context.Background())

	// 工单超时升级定时任务(复用 leasing 的 Redis 锁防重模式): 启动补偿 + 周期扫描.
	stopEsc := cron.Start(context.Background(), ctx)
	defer stopEsc()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
