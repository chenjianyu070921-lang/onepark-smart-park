package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/workorder-service/internal/config"
	"onepark/app/workorder-service/internal/consumer"
	"onepark/app/workorder-service/internal/cron"
	"onepark/app/workorder-service/internal/handler"
	"onepark/app/workorder-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/workorder-api.yaml", "the config file")

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

	// 告警自动建单消费者: 消费 alarm-event → 幂等建报修工单(alarm_id 唯一键去重).
	// Kafka.Enabled=false 时 Start 内部直接跳过; 注意 Consumer.Consume 阻塞运行,
	// 必须放在独立 goroutine 中, 否则会阻塞主线程导致 HTTP 服务起不来.
	go consumer.NewAlarmRunner(c.Kafka, ctx.DB, ctx.Producer).Start(context.Background())

	// 工单超时升级定时任务(复用 leasing 的 Redis 锁防重模式): 启动补偿 + 周期扫描.
	stopEsc := cron.Start(context.Background(), ctx)
	defer stopEsc()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
