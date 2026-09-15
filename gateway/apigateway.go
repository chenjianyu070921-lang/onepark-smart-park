package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"

	commonmw "onepark/common/middleware"
	"onepark/gateway/internal/config"
	"onepark/gateway/internal/middleware"
	"onepark/gateway/internal/proxy"
	"onepark/gateway/internal/svc"
)

var configFile = flag.String("f", "etc/apigateway-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	svcCtx := svc.NewServiceContext(c)

	// middleware 链(由外到内执行): RequestId -> AccessLog -> Auth -> RateLimit -> proxy
	handler := proxy.NewHandler(c.Upstreams)
	handler = middleware.RateLimit(svcCtx.Redis, c.RateLimit.Capacity, c.RateLimit.RatePerSec)(handler)
	skip := make(map[string]bool, len(c.AuthSkipPaths))
	for _, p := range c.AuthSkipPaths {
		skip[p] = true
	}
	handler = middleware.Auth(c.JwtSecret, skip)(handler)
	handler = middleware.AccessLog(handler)
	handler = commonmw.RequestIdMiddleware(handler)

	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	logx.Infof("apigateway listening on %s", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	logx.Must(server.ListenAndServe())
}
