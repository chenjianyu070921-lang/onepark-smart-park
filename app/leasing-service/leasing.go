package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/cron"
	"onepark/app/leasing-service/internal/handler"
	"onepark/app/leasing-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/leasing-api.yaml", "the config file")

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
	// 下游只透传: JWT 校验/租户注入已收口到网关(见 gateway/internal/middleware.Auth),
	// 本服务仅通过 IdentityFromHeader 提升网关注入的身份 Header.
	server.Use(middleware.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 定时任务: 启动补偿 + 每日 00:05(合同续约/到期) + 每月 1 号 00:10(上月经租账单出账)。
	// 两者都用 Redis 分布式锁防多实例重复。
	cronCtx, cancelCron := context.WithCancel(context.Background())
	defer cancelCron()
	stopCron := cron.Start(cronCtx, ctx)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
