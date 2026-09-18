package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"onepark/app/dashboard-service/internal/config"
	"onepark/app/dashboard-service/internal/handler"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/wshub"
	"onepark/app/dashboard-service/internal/wsserver"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/dashboard-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件顺序有讲究: Cors 必须在 JWT 外层 —— 浏览器的 OPTIONS 预检不带 token,
	// 若 JWT 在外层会直接把预检判成 401, 前端所有跨域请求都会失败。
	// Cors 对 OPTIONS 直接返回 204 并中断, 不会走到 JWT。
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	// JWT 鉴权: 同时覆盖 HTTP 接口与 /ws/dashboard(WS 走 ?token=, extractToken 已支持)
	server.Use(middleware.JWT(c.JwtSecret))

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 大屏 WebSocket 实时推送(清单 #73):
	// 路由用 AddRoute 程序化注册而非写进 .api —— goctl 不支持 WS 升级,
	// 且不改动 routes.go, 重新生成不会互相覆盖。
	hub := wshub.NewHub()
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/ws/dashboard",
		Handler: wsserver.Handler(hub),
	})

	wsCtx, cancelWs := context.WithCancel(context.Background())
	defer cancelWs()

	// 事件增量推送(组长计划书 周四 P0): 消费 Kafka 告警/工单事件, 到达即广播增量,
	// 并由快照循环失效聚合缓存后重新聚合。默认关闭 —— 见 config.KafkaConf 注释。
	dirty := wsserver.NewDirty()
	for _, runner := range wsserver.StartEventConsumers(wsCtx, c, hub, dirty) {
		runner := runner
		defer func() { _ = runner.Close() }()
		go runner.Start(wsCtx)
	}

	// 周期快照广播(同时消费 dirty 标记做缓存失效); 随进程退出
	go wsserver.StartSnapshotPush(wsCtx, ctx, hub, dirty)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
