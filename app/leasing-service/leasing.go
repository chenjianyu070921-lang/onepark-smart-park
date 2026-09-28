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
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/leasing-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// conf.UseEnv() 必填: go-zero 的 ${VAR} 环境变量展开默认是关闭的, 不启用则占位符是死字符串。
	// etc/leasing-api.yaml 的注释写着"部署环境请改回 ${LEASING_MYSQL_DSN} 形式", 不启用这句话就是假的 ——
	// 届时 DSN 会带着未展开的 ${...} 去连库, 只报出一句难懂的 invalid DSN。
	// 全平台多数服务(workorder/billing/alarm/video/...)以及本服务的 gRPC 入口都已启用, 此处补齐。
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一响应体 {code,msg,data}: 本服务 handler 走 httpx.OkJsonCtx 返回裸 payload,
	// 依赖全局 OkHandler 包装. 未注册时接口直接返回裸对象(如 {"total":0,"list":[]}),
	// 前端 axios 拦截器取不到 code 字段会误判为失败.
	// 与 auth-service / user-manage / energy-analysis 保持一致.
	response.Init()

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
