// Package redisx 提供 go-redis 初始化封装, 供各业务服务复用.
// 当前仅封装客户端创建, 业务服务在 ServiceContext 中统一初始化并注入 logic.
package redisx

import "github.com/redis/go-redis/v9"

// RedisConf 定义 Redis 连接配置, 与 go-zero 配置加载保持一致.
type RedisConf struct {
	Addr string // Redis 地址, 如 host:port
	Pass string // 密码, 无密码时为空
	DB   int    `json:",default=0"` // 数据库编号, 默认 0
}

// Client 是 *redis.Client 的别名, 避免各服务直接依赖 go-redis 版本.
type Client = redis.Client

// NewClient 根据配置创建 Redis 客户端.
// 注意: 创建客户端不会立即建立 TCP 连接, 首次命令时才会真正拨号.
func NewClient(conf *RedisConf) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     conf.Addr,
		Password: conf.Pass,
		DB:       conf.DB,
	})
}
