package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"onepark/app/notice-service/internal/config"
	"onepark/app/notice-service/internal/consumer"
	"onepark/app/notice-service/internal/cron"
	"onepark/app/notice-service/internal/handler"
	"onepark/app/notice-service/internal/svc"
	"onepark/common/health"
	cmw "onepark/common/middleware"
	"onepark/common/redisx"

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

	// 健康探针(P1): 复用项目统一 common/health 包, 与 workorder-service 同模式:
	// /api/healthz 存活(不探依赖) + /api/readyz 就绪(探 MySQL/Redis, 未配置项跳过).
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
		{Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(ctx.DB, readyRedis(ctx.Redis, c.Redis.Addr))},
	})

	// 站内通知消费者: 消费 workorder-event → 幂等生成 notice + notice_read 送达记录.
	// Start 为阻塞消费循环, 必须独立 goroutine 常驻(否则会卡住下方 server.Start);
	// Kafka.Enabled=false 或未配置时 Start 内部直接跳过.
	go consumer.NewWorkorderRunner(c.Kafka, ctx.DB).Start(context.Background())

	// 公告推送消费者: 消费 notice-event → Redis PubSub 推送在线用户(公告实时触达).
	// 独立消费组/独立开关(NoticeEvent 段), 与工单通知消费互不影响.
	go consumer.NewNoticeEventRunner(c.NoticeEvent, ctx.Redis).Start(context.Background())

	// 定时发布调度(P1): 周期扫描到达 publish_at 的草稿公告, 自动发布并投递 notice-event;
	// 独立开关(PublishCron 段), 启动补偿 + 多实例条件更新防重.
	stopCron := cron.Start(context.Background(), ctx)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}

// readyRedis 就绪探针的 Redis 归一: svc 恒创建 Redis 客户端(即使 Addr 为空),
// 未配置 Addr 时返回 nil 让 common/health.Readiness 按"未配置跳过"处理,
// 避免本地无 Redis 部署形态 readyz 恒 degraded.
func readyRedis(rdb *redisx.Client, addr string) *redisx.Client {
	if addr == "" {
		return nil
	}
	return rdb
}
