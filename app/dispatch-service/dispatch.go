package main

import (
	"flag"
	"fmt"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/handler"
	"onepark/app/dispatch-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/dispatch-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 中间件: CORS + RequestId 透传.
	server.Use(cmw.Cors)
	server.Use(cmw.RequestIdMiddleware)

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting dispatch-service at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
