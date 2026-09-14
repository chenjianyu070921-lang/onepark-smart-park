package svc

import (
	"onepark/app/video-service/internal/config"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// ServiceContext 全局依赖注入点 (M3 首个落地者, 见 KI-4).
// Day2 仅落地 Redis (go-zero 内置, 零额外依赖, 连接惰性);
// MySQL(GORM)/Kafka/ES/WebSocket 客户端在各自 Day 接入,
// 避免在中间件未就绪时导致启动失败.
type ServiceContext struct {
	Config config.Config
	Redis  *redis.Redis
}

// NewServiceContext 构造依赖.
func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{
		Config: c,
		Redis:  redis.MustNewRedis(c.Redis),
	}
}
