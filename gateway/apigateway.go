// Command apigateway 是 OnePark 对外统一 HTTP 入口(端口 8080).
// 采用"前缀反向代理"模式: 将所有未显式注册的路径交给 Gateway 处理,
// 按 etc/apigateway-api.yaml 中 Upstreams 配置的最长前缀转发到各业务服务,
// 并注入 RequestId 与 RBAC 上下文(x-tenant-id/x-user-id/x-role-ids).
package main

import (
	"flag"
	"fmt"

	"onepark/gateway/internal/config"
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

	// WithNotFoundHandler: 所有未匹配显式路由的请求交给网关代理处理.
	server := rest.MustNewServer(c.RestConf, rest.WithNotFoundHandler(ctx.Gateway))
	defer server.Stop()

	fmt.Printf("Starting gateway at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
