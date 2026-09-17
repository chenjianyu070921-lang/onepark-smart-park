package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/billing-service/internal/config"
	"onepark/app/billing-service/internal/cron"
	"onepark/app/billing-service/internal/handler"
	"onepark/app/billing-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/billing-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// UseEnv: yaml 里的 ${VAR} 占位需要环境变量替换, 默认(不加)会保留字面量导致 DSN 无效.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传 + RBAC 上下文.
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)
	server.Use(cmw.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 月度自动出账定时任务(复用 leasing 的 Redis 锁防重模式): 启动补偿 + 周期出账.
	stopCron := cron.Start(context.Background(), ctx)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
