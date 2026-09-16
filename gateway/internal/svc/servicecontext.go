package svc

import (
	"log"

	"onepark/gateway/internal/config"
	"onepark/gateway/internal/discovery"
	"onepark/gateway/internal/proxy"
)

// ServiceContext 持有网关运行时依赖.
type ServiceContext struct {
	Config  config.Config
	Gateway *proxy.Gateway // 前缀反向代理(统一转发到各业务服务)
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
	return &ServiceContext{
		Config:  c,
		Gateway: g,
	}
}
