package main

import (
	"flag"
	"fmt"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/handler"
	"onepark/app/access-control-service/internal/svc"
	"onepark/common/response"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/accesscontrol-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
