package main

import (
	"flag"
	"fmt"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/handler"
	"onepark/app/access-control-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/accesscontrol-api.yaml", "the config file")

func main() {
	flag.Parse()

	// yaml 中 ${VAR} 占位由环境变量展开(M2 各服务同款); 不调用则占位符按字面量处理.
	conf.UseEnv()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域 + 租户上下文注入(RBAC 行级隔离依赖 x-tenant-id).
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	server.Use(middleware.Tenant)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
