package config

import (
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 dashboard-service 的运行配置.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // MySQL 连接配置(dashboard_db)
	Redis redisx.RedisConf // Redis 连接配置(聚合缓存)

	// JwtSecret 与网关/auth-service 共用的 JWT 密钥, 用于校验大屏 WebSocket ?token= 接入.
	// 必须来自环境变量 ${AUTH_SECRET}, 禁止硬编码.
	JwtSecret string `json:",optional"`

	// Workorder 是 M2 workorder-service 的 gRPC 客户端配置(大屏聚合数据源).
	// 按 docs/服务协议规范, 服务间调用用 gRPC, 不走 HTTP.
	Workorder zrpc.RpcClientConf

	// Alarm 是 M3 alarm-service 的 gRPC 客户端配置(大屏活跃告警卡片, 清单 #43).
	Alarm zrpc.RpcClientConf
}
