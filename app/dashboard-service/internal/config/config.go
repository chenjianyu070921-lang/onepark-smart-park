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

	// 以下为四个上游数据源的 gRPC 客户端配置(大屏聚合数据源).
	// 按 docs/服务协议规范, 服务间调用一律用 gRPC, 不走 HTTP.
	// 未配置(Endpoints/Target/Etcd 均为空)时对应端口退化为 NotReady 占位, 该卡片降级为 null。
	Device    zrpc.RpcClientConf // M1 device-service      设备卡片
	Workorder zrpc.RpcClientConf // M2 workorder-service   工单卡片
	Alarm     zrpc.RpcClientConf // M3 alarm-service       告警卡片
	Energy    zrpc.RpcClientConf // M4 energy-data-service 能耗卡片

	// JwtSecret HTTP/WebSocket 鉴权密钥; 为空时放行。
	// WebSocket 场景依赖它校验 ?token=(浏览器无法给 WS 握手设置 Authorization 头)。
	JwtSecret string `json:",env=JWT_SECRET,optional"`
}
