// Command apigateway 是 OnePark 对外统一 HTTP 入口(端口 8080).
// 采用"前缀反向代理"模式: 将所有未显式注册的路径交给 Gateway 处理,
// 按 etc/apigateway-api.yaml 中 Upstreams 配置的最长前缀转发到各业务服务,
// 并注入 RequestId 与 RBAC 上下文(x-tenant-id/x-user-id/x-role-ids).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"onepark/gateway/internal/config"
	"onepark/gateway/internal/middleware"
	"onepark/gateway/internal/svc"
	"onepark/common/health"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/apigateway-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	ctx := svc.NewServiceContext(c)

	gw := ctx.Gateway
	// 中间件责任链: 代理处理器 -> (鉴权) -> (限流). 先鉴权后限流, 让限流在已识别身份之后生效.
	final := gw.ServeHTTP
	// 网关统一鉴权: 本地(dev/test)默认关闭(便于无 token 联调); 非本地(prod/pre 等)强制开启,
	// 落实"JWT 校验/租户注入收口到网关", 下游业务服务仅透传身份 Header.
	authEnabled := c.Auth.Enabled || !isLocalMode(c.Mode)
	if authEnabled {
		if ctx.AuthClient == nil {
			log.Fatalf("gateway: 鉴权已启用但 Auth.GrpcAddress 为空 (set AUTH_GRPC_ADDR or Auth.GrpcAddress)")
		}
		// 全接口强制校验: 委托 auth-service gRPC Verify, 仅鉴权引导端点(login/refresh/verify/logout)公开, 见 middleware.Auth.
		// 统一 RBAC: 鉴权后、转发前对受保护写操作做权限校验(委托 user-manage gRPC CheckPermission, Redis 缓存).
		// 仅当配置了 user-manage gRPC 地址时生效; 未配置则全部放行(渐进覆盖, 不破坏可用性).
		// 注意包裹顺序: 后套的中间件在外层先执行. Permission 依赖 Auth 注入的 x-user-id,
		// 因此必须先应用 Permission(内层), 再应用 Auth(外层), 才能保证"先鉴权后鉴权权限"的执行顺序.
		if ctx.UserClient != nil {
			final = middleware.Permission(ctx.UserClient, ctx.Redis)(final)
		}
		final = middleware.Auth(ctx.AuthClient)(final)
	}
	// 全局令牌桶限流(单 IP): 需配置 Redis 且 Capacity>0; 否则优雅降级放行, 不影响可用性.
	rateLimited := false
	if c.RateLimit.Capacity > 0 && ctx.Redis != nil {
		final = middleware.RateLimit(ctx.Redis, c.RateLimit.Capacity, c.RateLimit.RatePerSec)(final)
		rateLimited = true
	}

	// WithNotFoundHandler: 所有未匹配显式路由的请求交给网关代理(已含鉴权/限流)处理.
	server := rest.MustNewServer(c.RestConf, rest.WithNotFoundHandler(http.HandlerFunc(final)))
	defer server.Stop()

	// 健康检查端点: 网关无本地 DB, 仅探测 Redis; 供 K8s/Docker 探针与运维使用.
	// /health 兼容旧探针; /api/healthz 存活(不探依赖), /api/readyz 就绪(探 Redis).
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/health", Handler: health.Handler(nil, ctx.Redis)},
		{Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
		{Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(nil, ctx.Redis)},
	})

	fmt.Printf("Starting gateway at %s:%d (auth=%v ratelimit=%v)...\n", c.Host, c.Port, authEnabled, rateLimited)
	server.Start()
}

// isLocalMode 本地联调(dev/test)返回 true: 网关鉴权在此类环境默认关闭, 便于无 token 联调;
// 其余环境(prod/pre 等)强制开启, 落实"JWT 校验/租权注入收口到网关".
// 空字符串(未配置 Mode)按本地处理, 避免本地无 AUTH_SECRET 时因鉴权被强制开启而启动崩溃.
func isLocalMode(mode string) bool {
	return mode == "" || mode == service.DevMode || mode == service.TestMode
}
