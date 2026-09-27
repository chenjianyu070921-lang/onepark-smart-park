package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	"onepark/app/billing-service/internal/config"
	"onepark/app/billing-service/internal/cron"
	"onepark/app/billing-service/internal/handler"
	grpcserver "onepark/app/billing-service/internal/server"
	"onepark/app/billing-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	billingpb "onepark/proto/billing"
)

var configFile = flag.String("f", "etc/billing-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// UseEnv: yaml 里的 ${VAR} 占位需要环境变量替换, 默认(不加)会保留字面量导致 DSN 无效.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传 + RBAC 上下文.
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)
	server.Use(cmw.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 月度自动出账定时任务(复用 leasing 的 Redis 锁防重模式): 启动补偿 + 周期出账.
	stopCron := cron.Start(context.Background(), ctx)
	defer stopCron()

	// 双模: 未配置 Grpc.ListenOn 时保持纯 HTTP(网关行为不变); 配置后同进程额外起 gRPC server.
	if c.Grpc.ListenOn == "" {
		fmt.Printf("Starting HTTP server at %s:%d...\n", c.Host, c.Port)
		server.Start()
		return
	}

	go func() {
		fmt.Printf("Starting HTTP server at %s:%d...\n", c.Host, c.Port)
		server.Start()
	}()

	lis, err := net.Listen("tcp", c.Grpc.ListenOn)
	if err != nil {
		log.Fatalf("billing-service: 启动 gRPC 监听 %s 失败: %v", c.Grpc.ListenOn, err)
	}
	gs := grpc.NewServer()
	billingpb.RegisterBillingServiceServer(gs, grpcserver.NewBillingServer(ctx))
	if c.Mode == service.DevMode || c.Mode == service.TestMode {
		reflection.Register(gs)
	}
	fmt.Printf("Starting gRPC server at %s...\n", c.Grpc.ListenOn)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("billing-service: gRPC server 异常退出: %v", err)
	}
}
