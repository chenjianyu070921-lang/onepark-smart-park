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

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/apigateway-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	ctx := svc.NewServiceContext(c)

	gw := ctx.Gateway
	var notFound http.Handler = gw
	// 网关统一鉴权(配置开关控制): 启用时校验 Bearer Token 并注入身份 Header,
	// 未命中白名单且无有效 Token 直接 401; 关闭时退化为默认身份注入(联调模式).
	if c.Auth.Enabled {
		if c.Auth.Secret == "" {
			log.Fatalf("gateway: Auth.Enabled=true but Auth.Secret is empty (set AUTH_SECRET or Auth.Secret)")
		}
		// 全接口强制 JWT: 仅鉴权引导端点(login/refresh/verify)公开, 见 middleware.Auth.
		notFound = middleware.Auth(c.Auth.Secret)(gw.ServeHTTP)
	}

	// WithNotFoundHandler: 所有未匹配显式路由的请求交给网关代理(已含鉴权)处理.
	server := rest.MustNewServer(c.RestConf, rest.WithNotFoundHandler(notFound))
	defer server.Stop()

	fmt.Printf("Starting gateway at %s:%d (auth=%v)...\n", c.Host, c.Port, c.Auth.Enabled)
	server.Start()
}
