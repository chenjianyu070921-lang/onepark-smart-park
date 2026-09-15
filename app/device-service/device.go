package main

import (
	"flag"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"onepark/app/device-service/internal/config"
	"onepark/app/device-service/internal/handler"
	"onepark/app/device-service/internal/server"
	"onepark/app/device-service/internal/svc"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

var configFile = flag.String("f", "etc/device-api.yaml", "the config file")

// device-service 同时提供 HTTP(:8001) 与 gRPC(:9001):
// HTTP 面向平台侧管理(注册/查询/指令), gRPC 面向服务间调用(M2/M3/M5).
func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	ctx := svc.NewServiceContext(c)
	defer ctx.Producer.Close()

	restServer := rest.MustNewServer(c.RestConf)
	defer restServer.Stop()
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

	fmt.Printf("Starting device-service: HTTP %s:%d, gRPC %s\n", c.Host, c.Port, c.Rpc.ListenOn)
	group.Start()
}
