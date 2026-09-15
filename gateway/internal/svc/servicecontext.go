package svc

import (
	"onepark/common/redisx"
	"onepark/gateway/internal/config"
)

// ServiceContext 网关运行时依赖. 当前仅注入可选 Redis(供限流使用).
type ServiceContext struct {
	Config config.Config
	Redis  *redisx.Client
}

// NewServiceContext 初始化网关依赖. Redis 仅在配置了 Addr 时连接, 否则为 nil(限流降级放行).
func NewServiceContext(c config.Config) *ServiceContext {
	svc := &ServiceContext{Config: c}
	if c.Redis.Addr != "" {
		svc.Redis = redisx.NewClient(&redisx.RedisConf{
			Addr: c.Redis.Addr,
			Pass: c.Redis.Pass,
			DB:   c.Redis.DB,
		})
	}
	return svc
}
