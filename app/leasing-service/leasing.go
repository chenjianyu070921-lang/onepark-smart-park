package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/cron"
	"onepark/app/leasing-service/internal/handler"
	"onepark/app/leasing-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/leasing-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 合同到期定时任务: 启动补偿一次 + 每日 00:05 执行(Redis 分布式锁防多实例重复)
	cronCtx, cancelCron := context.WithCancel(context.Background())
	defer cancelCron()
	stopCron := cron.Start(cronCtx, ctx.DB, ctx.Redis)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
