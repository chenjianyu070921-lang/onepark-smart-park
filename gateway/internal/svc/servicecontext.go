package svc

import (
	"log"

	"onepark/common/redisx"
	"onepark/gateway/internal/config"
	"onepark/gateway/internal/discovery"
	"onepark/gateway/internal/proxy"
)

// ServiceContext 持有网关运行时依赖.
type ServiceContext struct {
	Config  config.Config
	Gateway *proxy.Gateway // 前缀反向代理(统一转发到各业务服务)
	Redis   *redisx.Client // 限流依赖; nil 表示未配置(限流降级放行)
}

// NewServiceContext 构建网关上下文; 上游地址非法时直接退出.
func NewServiceContext(c config.Config) *ServiceContext {
	g, err := proxy.NewGateway(c)
	if err != nil {
		log.Fatalf("init gateway proxy failed: %v", err)
	}
	// 可选: 从 Nacos 配置中心动态拉取上游表(仅当 Nacos.Address 配置时).
	if c.Nacos.Address != "" {
		go discovery.StartWatch(c.Nacos, g)
	}
	// 可选: 限流 Redis; 仅当配置了 Addr 才初始化(未配置时限流降级放行).
	var rdb *redisx.Client
	if c.Redis.Addr != "" {
		rdb = redisx.NewClient(&c.Redis)
	}
	return &ServiceContext{
		Config:  c,
		Gateway: g,
		Redis:   rdb,
	}
}
