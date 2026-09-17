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
	// JWT 鉴权: secret 为空时透传(开发期), 只在配置里填了密钥才真正校验
	server.Use(middleware.JWT(c.JwtSecret))

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 合同到期定时任务: 启动补偿一次 + 每日 00:05 执行(Redis 分布式锁防多实例重复)
	cronCtx, cancelCron := context.WithCancel(context.Background())
	defer cancelCron()
	stopCron := cron.Start(cronCtx, ctx.DB, ctx.Redis)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
