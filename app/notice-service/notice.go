package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/notice-service/internal/config"
	"onepark/app/notice-service/internal/consumer"
	"onepark/app/notice-service/internal/handler"
	"onepark/app/notice-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/notice-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传 + RBAC 上下文(网关注入 x-tenant-id/x-user-id/x-role-ids).
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)
	server.Use(cmw.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 站内通知消费者: 消费 workorder-event → 幂等生成 notice + notice_read 送达记录.
	// 独立 goroutine 常驻; Kafka.Enabled=false 或未配置时 Start 内部直接跳过.
	consumer.NewWorkorderRunner(c.Kafka, ctx.DB).Start(context.Background())

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
