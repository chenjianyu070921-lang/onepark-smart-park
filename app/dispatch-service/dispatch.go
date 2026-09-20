package main

import (
	"context"
	"flag"
	"fmt"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/consumer"
	"onepark/app/dispatch-service/internal/cron"
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
	// conf.UseEnv() 必填: go-zero 的 ${VAR} 环境变量展开默认是关闭的, 不启用则占位符是死字符串。
	// etc/dispatch-api.yaml 的注释写着"部署环境请改回 ${DISPATCH_MYSQL_DSN} 形式", 不启用这句话就是假的 ——
	// 届时 DSN 会带着未展开的 ${...} 去连库, 只报出一句难懂的 invalid DSN。
	// 容器部署(deploy/m5)依赖它注入连接目标, 故必须启用。
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件顺序有讲究: Cors 必须在 JWT 外层 —— 浏览器的 OPTIONS 预检不带 token,
	// 若 JWT 在外层会直接把预检判成 401, 前端所有跨域请求都会失败。
	// Cors 对 OPTIONS 直接返回 204 并中断, 不会走到 JWT。
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	// JWT 鉴权: 填了密钥后, 审计流水的 operator_id 才会从 token 的 userId claim 取值
	server.Use(middleware.JWT(c.JwtSecret))

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// 告警自动建单消费者(接口清单 #74)。默认关闭 —— 见 config.KafkaConf 注释。
	consumerCtx, stopConsumer := context.WithCancel(context.Background())
	defer stopConsumer()
	if runner := consumer.NewRunner(c, ctx.DB); runner != nil {
		defer func() { _ = runner.Close() }()
		go runner.Start(consumerCtx)
	}

	// 指派超时重派: 启动补偿一次 + 每 IntervalSec 扫一次(Redis 分布式锁防多实例重复改派)。
	// 该任务只读写本服务自己的库, 不碰共享设施, 因此没有开关(见 config.ReassignConf 注释)。
	cronCtx, cancelCron := context.WithCancel(context.Background())
	defer cancelCron()
	stopCron := cron.Start(cronCtx, ctx.DB, ctx.Redis, c.Reassign.IntervalSec, c.Reassign.MaxReassign)
	defer stopCron()

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
