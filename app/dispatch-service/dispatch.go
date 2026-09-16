package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/consumer"
	"onepark/app/dispatch-service/internal/cron"
	"onepark/app/dispatch-service/internal/handler"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/dispatch-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	server.Use(middleware.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 告警自动建单消费者(接口清单 #74)。默认关闭 —— 见 config.KafkaConf 注释。
	consumerCtx, stopConsumer := context.WithCancel(context.Background())
	defer stopConsumer()
	if runner := consumer.NewRunner(c, ctx.DB); runner != nil {
		defer func() { _ = runner.Close() }()
		go runner.Start(consumerCtx)
	}

	// 指派超时重派扫描: 已指派超时未接单的工单退回待指派。
	// 多实例安全 —— 逐单乐观锁抢占, 无需分布式锁。
	cronCtx, stopCron := context.WithCancel(context.Background())
	defer stopCron()
	go cron.NewExpireScanner(ctx.DB).Start(cronCtx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
