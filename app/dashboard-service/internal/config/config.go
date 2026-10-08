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

	// Kafka 大屏事件增量推送(组长计划书 周四 P0)。
	Kafka KafkaConf `json:",optional"`

	// JwtSecret HTTP/WebSocket 鉴权密钥; 为空时放行。
	// WebSocket 场景依赖它校验 ?token=(浏览器无法给 WS 握手设置 Authorization 头)。
	JwtSecret string `json:",env=JWT_SECRET,optional"`

	// Ws 大屏 WebSocket 的接入来源与多实例扇出配置。
	Ws WsConf `json:",optional"`
}

// WsConf 大屏 WebSocket 配置。
type WsConf struct {
	// AllowOrigins 握手来源白名单, 形如 `http://localhost:5173` / `https://dash.example.com`。
	// 按 scheme://host[:port] 精确比对(忽略末尾斜杠与 path)。
	//
	// ⚠️ 留空**不等于放开**: 留空 = 只允许**同源**(Origin 的 host 与请求 Host 相同)
	// 以及**非浏览器客户端**(握手不带 Origin, 如压测工具/服务端订阅)。
	// 值为 ["*"] 表示放开一切, 仅供本机联调 —— 启动时会打 warn(见 logAllowOrigins)。
	//
	// 为什么必须配: 浏览器的 WebSocket **不受同源策略限制**, 任意站点都能借用户浏览器
	// 连上实时通道(跨站 WebSocket 劫持)。上线前必须收敛为真实域名白名单。
	AllowOrigins []string `json:",optional"`

	// Fanout 是否开启 Redis Pub/Sub 多实例扇出。
	// 默认 false = 单实例内存广播(与引入扇出之前行为一致)。
	// 多实例部署必须开启: 否则每个实例只能把消息推给自己进程内的连接,
	// 连到 A 实例的大屏收不到 B 实例产生的消息(表现为"大屏时好时坏")。
	Fanout bool `json:",default=false"`

	// FanoutChannel 扇出频道名。多实例之间必须**一致**; 不能与其它业务频道重名。
	FanoutChannel string `json:",default=onepark:dashboard:ws"`
}

// KafkaConf 大屏事件消费配置.
//
// ⚠️ 与 dispatch-service 同样的约束 —— 本项目用的是**多项目共用的 broker**:
//
//  1. Group 必须形如 m5-dashboard-{env}-{owner}, 本服务会再拼上 topic 名。
//     Kafka 位移按 (group, topic) 提交, 与 M2/M3 的消费组重名会把它们该消费的消息吃掉。
//     本服务是"旁听者": 用的是自己的独立 group, 不与他人抢消息。
//
//  2. Enabled 默认 false, 必须显式开启。开启前需确认目标 topic 存在且属于 onepark 体系
//     (broker 若开 auto.create.topics.enable, 消费不存在的 topic 会隐式建 topic = 向共享设施写入)。
//
//  3. Topics 必须显式配置, 代码里**不猜默认值** —— 共享 broker 上猜错就是读别人的消息。
type KafkaConf struct {
	Brokers string   `json:",optional"`
	Topics  []string `json:",optional"`
	Group   string   `json:",default=m5-dashboard-dev"`
	Enabled bool     `json:",default=false"`
}
