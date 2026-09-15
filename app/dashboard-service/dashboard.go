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

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)

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

	// 周期快照广播; 随进程退出
	wsCtx, cancelWs := context.WithCancel(context.Background())
	defer cancelWs()
	go wsserver.StartSnapshotPush(wsCtx, ctx, hub)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
