package main

import (
	"flag"
	"fmt"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/handler"
	"onepark/app/video-service/internal/svc"
	"onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/video-api.yaml", "the config file")

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
	// 网关注入的租户/操作人身份写入 context(摄像头数据按租户隔离)
	server.Use(middleware.ContextMiddleware)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
