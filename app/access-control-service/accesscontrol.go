package main

import (
	"flag"
	"fmt"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/handler"
	"onepark/app/access-control-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/accesscontrol-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// conf.UseEnv() 必填: go-zero 的 ${VAR} 环境变量展开默认是关闭的, 不启用则占位符是死字符串.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 全链路 RequestId 透传 + 开发环境跨域
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	// 网关注入的租户/操作人身份写入 context(远程开门需要 operator_id 落审计)
	server.Use(middleware.ContextMiddleware)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
