package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/parking-service/internal/config"
	"onepark/app/parking-service/internal/handler"
	"onepark/app/parking-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/parking-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传 + RBAC 上下文(网关注入 x-tenant-id/x-user-id/x-role-ids).
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)
	server.Use(cmw.Tenant)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)
	// 启动后台 Kafka 消费者(地磁遥测 -> 停车记录), 主服务退出时随进程结束.
	ctx.StartConsumers(context.Background())

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
