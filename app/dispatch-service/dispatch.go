package main

import (
	"flag"
	"fmt"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/handler"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/dispatch-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
