package config

import (
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 dashboard-service 运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis 与 workorder gRPC 客户端配置.
type Config struct {
	rest.RestConf
	MySQL       gormx.MySQLConf  // MySQL 连接配置(预留, 后续看板本地聚合可用)
	Redis       redisx.RedisConf // Redis 连接配置(概览缓存)
	WorkorderRPC zrpc.RpcClientConf // workorder gRPC 客户端(M2 工单数据源)
}
