// Package redisx 提供 go-redis 初始化封装, 供各业务服务复用.
// 当前仅封装客户端创建, 业务服务在 ServiceContext 中统一初始化并注入 logic.
package redisx

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConf 定义 Redis 连接配置, 与 go-zero 配置加载保持一致.
type RedisConf struct {
	Addr         string `json:",default="` // Redis 地址, 如 host:port
	Pass         string `json:",default="` // 密码, 无密码时为空
	DB           int    `json:",default=0"` // 数据库编号, 默认 0
	PoolSize     int    `json:",default=0"` // 连接池最大连接数; 0 表示 go-redis 默认(10*CPU)
	MinIdleConns int    `json:",default=0"` // 最小空闲连接; 0 表示不保活
	PoolTimeout  int    `json:",default=0"` // 获取连接超时(毫秒); 0 表示默认 4s
	IdleTimeout  int    `json:",default=0"` // 空闲连接回收时间(毫秒); 0 表示默认 5min
}

// Client 是 *redis.Client 的别名, 避免各服务直接依赖 go-redis 版本.
type Client = redis.Client

// NewClient 根据配置创建 Redis 客户端.
// 注意: 创建客户端不会立即建立 TCP 连接, 首次命令时才会真正拨号.
func NewClient(conf *RedisConf) *redis.Client {
	opts := &redis.Options{
		Addr:         conf.Addr,
		Password:     conf.Pass,
		DB:           conf.DB,
		PoolSize:     conf.PoolSize,
		MinIdleConns: conf.MinIdleConns,
	}
	if conf.PoolTimeout > 0 {
		opts.PoolTimeout = time.Duration(conf.PoolTimeout) * time.Millisecond
	}
	if conf.IdleTimeout > 0 {
		opts.ConnMaxIdleTime = time.Duration(conf.IdleTimeout) * time.Millisecond
	}
	return redis.NewClient(opts)
}
