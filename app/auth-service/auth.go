package main

import (
	"flag"
	"fmt"

	"onepark/app/auth-service/internal/config"
	"onepark/app/auth-service/internal/handler"
	"onepark/app/auth-service/internal/middleware"
	"onepark/app/auth-service/internal/svc"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/auth-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	// JWT 鉴权中间件: 校验受保护路由的 Bearer Token 并注入 ctxdata.
	server.Use(middleware.JwtAuth(ctx))
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
