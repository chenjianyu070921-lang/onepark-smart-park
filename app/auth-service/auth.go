package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	"onepark/app/auth-service/internal/config"
	"onepark/app/auth-service/internal/handler"
	grpcserver "onepark/app/auth-service/internal/server"
	"onepark/common/middleware"
	"onepark/app/auth-service/internal/svc"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	authpb "onepark/proto/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/auth-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 启动强校验: 非本地(dev/test)环境下 JwtSecret 必须显式配置, 为空则拒绝启动.
	validateJwtSecret(c)

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	// 下游只透传: JWT 校验/租户注入已收口到网关, 本服务仅从网关注入的身份 Header 提升 ctxdata.
	server.Use(middleware.IdentityFromHeader)
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
		log.Fatalf("auth-service: 启动 gRPC 监听 %s 失败: %v", c.Grpc.ListenOn, err)
	}
	gs := grpc.NewServer()
	authpb.RegisterAuthServiceServer(gs, grpcserver.NewAuthServer(ctx))
	if c.Mode == service.DevMode || c.Mode == service.TestMode {
		reflection.Register(gs)
	}
	fmt.Printf("Starting gRPC server at %s...\n", c.Grpc.ListenOn)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("auth-service: gRPC server 异常退出: %v", err)
	}
}

// validateJwtSecret 启动强校验 JWT 密钥: 无论何种环境, 空密钥一律拒绝启动(fail-closed).
// auth-service 负责签发令牌(login/refresh), 空密钥会导致令牌不可校验、鉴权形同虚设;
// 故失败即拒绝启动. 本地联调也须设置环境变量 JWT_SECRET(任意 dev 值即可),
// 与"网关 dev 不强制鉴权、但本服务仍需密钥签发"的设计一致.
func validateJwtSecret(c config.Config) {
	if c.JwtSecret == "" {
		log.Fatalf("auth-service: JwtSecret 为空, 禁止启动; 请配置环境变量 JWT_SECRET")
	}
}
