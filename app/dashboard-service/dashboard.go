package main

import (
	"flag"
	"fmt"

	"onepark/app/dashboard-service/internal/config"
	"onepark/app/dashboard-service/internal/handler"
	dmw "onepark/app/dashboard-service/internal/middleware"
	"onepark/app/dashboard-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/dashboard-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: 租户注入(供 RBAC 隔离/缓存维度) + CORS + RequestId 透传.
	server.Use(dmw.Tenant)
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting dashboard-service at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
