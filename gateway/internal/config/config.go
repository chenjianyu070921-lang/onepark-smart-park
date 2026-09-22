package config

import (
	"time"

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
	// User user-manage gRPC 配置(供 RBAC CheckPermission 调用). 鉴权未启用时本段无需配置.
	User UserConf `json:",optional"`
	// Nacos 可选: 配置中心动态上游表. Address 为空时忽略, 继续使用上方静态 Upstreams.
	Nacos NacosConf `json:",optional"`
	// Redis 限流/缓存依赖(可选); 未配置时网关限流优雅降级为放行.
	Redis redisx.RedisConf `json:",optional"`
	// RateLimit 网关全局令牌桶限流(单 IP 维度); Capacity<=0 关闭.
	RateLimit RateLimitConf `json:",optional"`
	// Breaker 全局熔断默认参数(所有上游共享同一套阈值/冷却, 见 internal/proxy/proxy.go);
	// 阈值<=0 或 冷却=0 时回落到默认 5 / 10s, 防止误配导致熔断失效.
	BreakerThreshold int           `json:",default=5"`   // 单上游连续失败达该值后断开(默认 5)
	BreakerCooldown  time.Duration `json:",default=10s"` // 断开后冷却时长, 之后进入半开探测(默认 10s)
}

// RateLimitConf 网关令牌桶限流配置(单 IP 维度).
type RateLimitConf struct {
	Capacity   int64   `json:",default=100"` // 桶容量: 单 IP 最多允许的突发请求数(默认 100)
	RatePerSec float64 `json:",default=100"` // 稳定补充速率(令牌/秒), 默认 100 QPS/IP
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
	// GrpcAddress auth-service gRPC 地址(Verify 校验入口), 形如 127.0.0.1:18088, 可由环境变量 AUTH_GRPC_ADDR 注入.
	// 开启鉴权时必填; 网关不再本地验签, 统一委托 auth-service.Verify(签名/过期/吊销一站式).
	GrpcAddress string `json:",optional"`
	// Secret 保留字段: 与 auth-service 共享的 JWT 签名密钥(供后续本地预校验/调试), 当前鉴权已委托 gRPC Verify.
	Secret string `json:",optional"`
}

// UserConf 网关调用 user-manage gRPC 的配置(RBAC CheckPermission 入口).
// 仅当鉴权启用(Auth.Enabled 或生产模式)且配置了 GrpcAddress 时, 网关才会对受保护写操作做权限校验;
// 未配置则 RBAC 不生效(全部放行), 不影响网关可用性.
type UserConf struct {
	// GrpcAddress user-manage gRPC 地址(CheckPermission 入口), 形如 127.0.0.1:18089, 可由环境变量 USER_GRPC_ADDR 注入.
	GrpcAddress string `json:",optional"`
}

// UpstreamConf 单条上游路由配置.
type UpstreamConf struct {
	Prefix string // 路径前缀, 如 /api/workorder
	Target string // 上游基址, 如 http://127.0.0.1:8082
	// CanaryTarget 灰度目标基址(可选); 非空时启用灰度, 按权重或 Header 将部分流量导向该实例.
	CanaryTarget string `json:",optional"`
	// CanaryWeight 灰度流量权重(0-100, 百分比); 默认 0 表示仅按 Header 命中才走灰度.
	CanaryWeight int `json:",optional"`
	// CanaryHeader 灰度命中 Header 名; 请求携带该 Header(值非 "false")即走灰度(可用于内部验证/定向).
	CanaryHeader string `json:",optional"`
	// BreakerThreshold 本路由级熔断阈值(可选); >0 时覆盖全局 BreakerThreshold,
	// 0/不填回落全局默认(与 NewGateway 的 <=0 收敛同源). 仅阈值可路由级覆盖, 冷却全局共享.
	BreakerThreshold int `json:",optional"`
}
