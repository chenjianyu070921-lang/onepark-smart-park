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
	// 下游只透传: JWT 校验/租户注入已收口到网关(含 /ws/dashboard 的 ?token= 由网关校验),
	// 本服务仅通过 IdentityFromHeader 提升网关注入的身份 Header; WS 直连/本地联调回退见 wsserver.Handler.
	server.Use(middleware.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 大屏 WebSocket 实时推送(清单 #73):
	// 路由用 AddRoute 程序化注册而非写进 .api —— goctl 不支持 WS 升级,
	// 且不改动 routes.go, 重新生成不会互相覆盖。
	hub := wshub.NewHub()
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/ws/dashboard",
		Handler: wsserver.Handler(hub, c.JwtSecret),
	})

	// 周期快照广播; 随进程退出
	wsCtx, cancelWs := context.WithCancel(context.Background())
	defer cancelWs()
	go wsserver.StartSnapshotPush(wsCtx, ctx, hub)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
