package main

import (
	"flag"
	"fmt"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/handler"
	"onepark/app/leasing-service/internal/svc"
	cmw "onepark/common/middleware"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/leasing-api.yaml", "the config file")

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

	fmt.Printf("Starting leasing-service at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
