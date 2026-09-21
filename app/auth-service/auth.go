package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"

	"onepark/app/auth-service/internal/config"
	"onepark/app/auth-service/internal/handler"
	grpcserver "onepark/app/auth-service/internal/server"
	"onepark/app/auth-service/internal/svc"
	"onepark/common/health"
	"onepark/common/middleware"
	"onepark/common/response"

	authpb "onepark/proto/auth"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/auth-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 启动强校验: 非本地环境(prod/pre 等) JwtSecret 为空则 fatal; 本地(dev/test)告警放行以便联调.
	validateJwtSecret(c)

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	// 下游只透传: JWT 校验/租户注入已收口到网关, 本服务仅从网关注入的身份 Header 提升 ctxdata.
	server.Use(middleware.IdentityFromHeader)
	handler.RegisterHandlers(server, ctx)

	// 健康检查: /api/healthz 存活(不探依赖), /api/readyz 就绪(探 MySQL + Redis 黑名单).
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
		{Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(ctx.DB, ctx.Redis)},
	})

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

// validateJwtSecret 启动强校验 JWT 密钥(落实遗留台账 #11):
//   - 非本地环境(prod/pre 等): 为空直接 fatal 拒绝启动, 避免空密钥签发/校验令牌导致鉴权形同虚设;
//   - 本地环境(dev/test): 为空仅告警并放行, 与"网关鉴权 dev 默认关、本地联调不打扰"保持一致.
func validateJwtSecret(c config.Config) {
	if c.JwtSecret != "" {
		return
	}
	if c.Mode == service.DevMode || c.Mode == service.TestMode {
		log.Printf("auth-service: [WARN] JwtSecret 为空, mode=%s 本地联调放行; 生产请配置环境变量 JWT_SECRET", c.Mode)
		return
	}
	log.Fatalf("auth-service: JwtSecret 为空, 非本地环境(mode=%s)禁止启动; 请配置环境变量 JWT_SECRET", c.Mode)
}
