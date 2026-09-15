package config

import "github.com/zeromicro/go-zero/rest"

// Config 定义对外网关(apigateway)运行配置.
// 网关职责: 路由转发(按前缀到各业务服务) + RequestId 注入 + RBAC 上下文透传.
type Config struct {
	rest.RestConf
	// Upstreams 路径前缀 -> 上游服务映射(最长前缀优先匹配).
	Upstreams []UpstreamConf `json:",optional"`
	// DefaultTenantId 无鉴权联调时的默认园区ID(生产由 M6 鉴权后经 Header 注入 x-tenant-id).
	DefaultTenantId int64 `json:",default=1"`
	// DefaultUserId 无鉴权联调时的默认用户ID(0 表示匿名).
	DefaultUserId int64 `json:",default=0"`
}

// UpstreamConf 单条上游路由配置.
type UpstreamConf struct {
	Prefix string // 路径前缀, 如 /api/workorder
	Target string // 上游基址, 如 http://127.0.0.1:8091
}
