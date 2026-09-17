package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"onepark/app/device-service/internal/config"
	"onepark/app/device-service/internal/cron"
	"onepark/app/device-service/internal/handler"
	"onepark/app/device-service/internal/mq"
	"onepark/app/device-service/internal/server"
	"onepark/app/device-service/internal/svc"
	"onepark/common/middleware"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

var configFile = flag.String("f", "etc/device-api.yaml", "the config file")

// device-service 同时提供 HTTP(:8001) 与 gRPC(:9001):
// HTTP 面向平台侧管理(注册/查询/指令), gRPC 面向服务间调用(M2/M3/M5).
// 常驻协程: 遥测消费(回写设备状态/影子/时序库) + 指令超时扫描.
func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	ctx := svc.NewServiceContext(c)
	defer ctx.Close()

	restServer := rest.MustNewServer(c.RestConf)
	restServer.Use(middleware.IdentityFromHeader)
	defer restServer.Stop()

	// 全局中间件: 请求ID -> 下游只透传(网关已校验并注入身份/租户 Header)
	restServer.Use(middleware.RequestIdMiddleware)

	handler.RegisterHandlers(restServer, ctx)

	rpcServer := zrpc.MustNewServer(c.Rpc, func(grpcServer *grpc.Server) {
		devicepb.RegisterDeviceServiceServer(grpcServer, server.NewDeviceServer(ctx))
		// 开启反射, 便于 grpcurl 联调 (grpcurl -plaintext localhost:9001 list)
		reflection.Register(grpcServer)
	})
	defer rpcServer.Stop()

	group := service.NewServiceGroup()
	group.Add(restServer)
	group.Add(rpcServer)

	// 后台常驻任务: 进程退出时通过 cancel 优雅停止
	taskCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		handler := mq.NewHandler(ctx)
		if err := handler.Consume(taskCtx, c.KafkaBrokers); err != nil && taskCtx.Err() == nil {
			logx.Errorf("遥测消费退出: %v", err)
		}
	}()

	go cron.NewCommandTimeoutTask(ctx, c.TimeoutScanIntervalSec, 200, 2).Start(taskCtx)

	fmt.Printf("Starting device-service: HTTP %s:%d, gRPC %s\n", c.Host, c.Port, c.Rpc.ListenOn)
	group.Start()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()
}
