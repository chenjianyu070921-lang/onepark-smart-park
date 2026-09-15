package config

import "github.com/zeromicro/go-zero/rest"

// Config 网关配置: 监听端口 + 业务服务 upstream 列表 + 限流 Redis.
// 注: 运行时使用原生 http.Server(RestConf 仅用于解析 Name/Host/Port), 不启用 go-zero rest 中间件机制.
type Config struct {
	rest.RestConf
	Upstreams []Upstream `json:",optional"`
	Redis      struct {
		Addr string `json:",optional"`
		Pass string `json:",optional"`
		DB   int    `json:",default=0"`
	} `json:",optional"`
	RateLimit struct {
		Capacity   int64   `json:",default=100"` // 令牌桶容量(突发上限)
		RatePerSec float64 `json:",default=20"`   // 每秒补充令牌数
	} `json:",optional"`
	// JwtSecret JWT 签名密钥, 必须与 auth-service 共用同一环境变量 JWT_SECRET(禁止硬编码).
	JwtSecret string `json:",optional"`
	// AuthSkipPaths 公开路径白名单(精确匹配), 不经过 JWT 校验, 如 /api/auth/login.
	AuthSkipPaths []string `json:",optional"`
}

// Upstream 业务服务地址: Name 用于匹配 URL 前缀, Port 为服务监听端口.
type Upstream struct {
	Name string `json:",optional"`
	Port int    `json:",optional"`
}
