package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"onepark/app/alarm-service/internal/config"
	"onepark/app/alarm-service/internal/handler"
	"onepark/app/alarm-service/internal/svc"
	"onepark/common/health"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/alarm-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// conf.UseEnv() 必填: go-zero 的 ${VAR} 环境变量展开默认是关闭的, 不启用则占位符是死字符串.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	// 网关注入的租户/操作人身份写入 context (见 middleware.ContextMiddleware 安全前提)
	server.Use(middleware.ContextMiddleware)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)
	// WebSocket 推送需 HTTP Upgrade, go-zero REST handler 的响应已被包装不可用,
	// 故用原生 http.HandlerFunc 挂载(docs/m3/09 §3); 注意 Use 中间件不作用于此路由.
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/ws/alarm", Handler: ctx.Hub.Handler()},
		// 健康检查: /api/healthz 存活(不探依赖), /api/readyz 就绪(探 MySQL + Redis).
		{Method: http.MethodGet, Path: "/api/healthz", Handler: health.Liveness()},
		{Method: http.MethodGet, Path: "/api/readyz", Handler: health.Readiness(ctx.DB, ctx.Redis)},
	})
	// 启动后台 Kafka 消费者(设备遥测 -> 安防告警), 主服务退出时随进程结束.
	ctx.StartConsumers(context.Background())
	// 启动 WebSocket 跨实例广播(Redis Pub/Sub), 多副本部署时各实例的大屏都能收到告警.
	ctx.StartWSRelay(context.Background())

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
