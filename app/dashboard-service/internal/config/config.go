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

	// Workorder 是 M2 workorder-service 的 gRPC 客户端配置(大屏聚合数据源).
	// 按 docs/服务协议规范, 服务间调用用 gRPC, 不走 HTTP.
	Workorder zrpc.RpcClientConf
}
