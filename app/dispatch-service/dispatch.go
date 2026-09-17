package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/consumer"
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

	// 中间件顺序有讲究: Cors 必须在 JWT 外层 —— 浏览器的 OPTIONS 预检不带 token,
	// 若 JWT 在外层会直接把预检判成 401, 前端所有跨域请求都会失败。
	// Cors 对 OPTIONS 直接返回 204 并中断, 不会走到 JWT。
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	// 下游只透传: JWT 校验/租户注入已收口到网关, 本服务仅提升网关注入的身份 Header.
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

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
