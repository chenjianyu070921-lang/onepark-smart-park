package main

import (
	"flag"
	"fmt"

	"onepark/app/user-manage/internal/config"
	"onepark/app/user-manage/internal/handler"
	"onepark/app/user-manage/internal/svc"
	"onepark/common/middleware"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/usermanage-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 将网关注入的身份 Header 提升进 ctxdata, 供 logic 层按登录用户鉴权/数据隔离.
	server.Use(middleware.IdentityFromHeader)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
