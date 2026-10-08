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
	// conf.UseEnv() 必填: go-zero 的 ${VAR} 环境变量展开默认是关闭的, 不启用则占位符是死字符串。
	// etc/dashboard-api.yaml 的注释写着"部署环境请改回 ${...} 形式", 不启用这句话就是假的 ——
	// 届时 DSN/上游地址会带着未展开的 ${...} 去连接, 只报出一句难懂的 invalid DSN。
	// 容器部署(deploy/m5)依赖它注入连接目标, 故必须启用。
	conf.MustLoad(*configFile, &c, conf.UseEnv())

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

	wsCtx, cancelWs := context.WithCancel(context.Background())
	defer cancelWs()

	// 多实例扇出: 默认关闭(单实例内存广播, 与引入扇出前行为一致)。
	// 开启后 Broadcast 走「本地优先 + 经 Redis 发布给其它实例」——
	// Redis 挂了只影响跨实例推送, 本实例在线的大屏照常刷新(可用性优先)。
	if c.Ws.Fanout {
		fanout := wshub.NewRedisFanout(ctx.Redis, c.Ws.FanoutChannel)
		hub.SetPublisher(wsCtx, fanout)
		go fanout.Run(wsCtx, hub)
		fmt.Printf("[ws] 多实例扇出已开启: channel=%s instance=%s\n", c.Ws.FanoutChannel, fanout.InstanceID())
	} else {
		fmt.Printf("[ws] 多实例扇出未开启(单实例内存广播) —— 多实例部署时须置 Ws.Fanout=true\n")
	}

	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/ws/dashboard",
		Handler: wsserver.Handler(hub, c.JwtSecret, c.Ws.AllowOrigins),
	})

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
