package config

import (
	"github.com/zeromicro/go-zero/rest"
	"onepark/common/redisx"
)

// Config 定义对外网关(apigateway)运行配置.
// 网关职责: 路由转发(按前缀到各业务服务) + RequestId 注入 + RBAC 上下文透传.
type Config struct {
	rest.RestConf
	// Upstreams 路径前缀 -> 上游服务映射(最长前缀优先匹配).
	Upstreams []UpstreamConf `json:",optional"`
	// DefaultTenantId 无鉴权联调时的默认园区ID(生产开启 Auth 后由 JWT 注入真实租户, 此值仅作兜底).
	DefaultTenantId int64 `json:",default=1"`
	// DefaultUserId 无鉴权联调时的默认用户ID(0 表示匿名).
	DefaultUserId int64 `json:",default=0"`
	// Auth 网关统一鉴权开关(默认关闭, 兼顾联调; 生产开启需与 auth-service 共享 JWT 密钥).
	Auth AuthConf `json:",optional"`
	// Nacos 可选: 配置中心动态上游表. Address 为空时忽略, 继续使用上方静态 Upstreams.
	Nacos NacosConf `json:",optional"`
	// Redis 限流/缓存依赖(可选); 未配置时网关限流优雅降级为放行.
	Redis redisx.RedisConf `json:",optional"`
	// RateLimit 网关全局令牌桶限流(单 IP 维度); Capacity<=0 关闭.
	RateLimit RateLimitConf `json:",optional"`
}

// RateLimitConf 网关令牌桶限流配置(单 IP 维度).
type RateLimitConf struct {
	Capacity   int64   `json:",default=200"` // 桶容量: 单 IP 最多允许的突发请求数
	RatePerSec float64 `json:",default=50"`  // 稳定补充速率(令牌/秒)
}

// NacosConf 网关上游表配置中心(仅替换"上游地址从哪来", 不改转发逻辑).
// 启用后网关从 Nacos 拉取 gateway-upstreams(JSON 数组 [{prefix,target}])并监听热更新;
// 拉取失败则回退到静态 Upstreams, 不影响原功能.
type NacosConf struct {
	Address   string `json:",default="` // nacos server, 形如 nacos:8848; 为空不启用
	Namespace string `json:",default="` // 命名空间 ID
	Group     string `json:",default=DEFAULT_GROUP"`
	DataId    string `json:",default=gateway-upstreams"`
	Username  string `json:",default=nacos"`
	Password  string `json:",default=nacos"`
}

// AuthConf 网关统一鉴权配置.
type AuthConf struct {
	// Enabled 是否启用 JWT 校验; 关闭时网关仅做默认身份注入(联调模式).
	Enabled bool `json:",default=false"`
	// Secret 与 auth-service 共享的 JWT 签名密钥; 开启鉴权时必填(可由环境变量 AUTH_SECRET 注入).
	Secret string `json:",optional"`
}

// UpstreamConf 单条上游路由配置.
type UpstreamConf struct {
	Prefix string // 路径前缀, 如 /api/workorder
	Target string // 上游基址, 如 http://127.0.0.1:8082
}
