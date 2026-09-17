package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	"onepark/app/user-manage/internal/config"
	"onepark/app/user-manage/internal/handler"
	grpcserver "onepark/app/user-manage/internal/server"
	"onepark/app/user-manage/internal/svc"
	"onepark/common/middleware"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	userpb "onepark/proto/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/usermanage-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 将网关注入的身份 Header 提升进 ctxdata, 供 logic 层按登录用户鉴权/数据隔离.
	server.Use(middleware.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

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
		log.Fatalf("user-manage: 启动 gRPC 监听 %s 失败: %v", c.Grpc.ListenOn, err)
	}
	gs := grpc.NewServer()
	userpb.RegisterUserManageServer(gs, grpcserver.NewUserManageServer(ctx))
	if c.Mode == service.DevMode || c.Mode == service.TestMode {
		reflection.Register(gs)
	}
	fmt.Printf("Starting gRPC server at %s...\n", c.Grpc.ListenOn)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("user-manage: gRPC server 异常退出: %v", err)
	}
}
