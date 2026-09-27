package config

import (
	"github.com/zeromicro/go-zero/rest"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 alarm-service 的运行配置(与 M2 四服务保持同构).
// MySQL/Redis/Kafka 在 ServiceContext 中初始化, 未配置时降级为空实现并以 WARN 提示, 不阻断启动.
type Config struct {
	rest.RestConf
	MySQL  gormx.MySQLConf  // alarm_db 连接配置
	Redis  redisx.RedisConf // Redis 配置(幂等去重 / 滑动窗口 / WS 跨实例广播)
	Kafka  KafkaConf        // Kafka 配置(消费 M1 设备遥测生成告警)
	WS     WSConf           // WebSocket 广播配置
	ES     ESConf           // Elasticsearch 配置(历史检索 #41 / 双写 #44)
	Notify NotifyConf       // 告警事件通知 M5(#40 解决告警后生产 Kafka 事件)
	// Dispatch 告警等级 → M5 工单优先级的映射配置(运营可按园区调整派单紧急度)
	Dispatch DispatchConf
	Rule     RuleConf  // 规则引擎相关开关
	// Tenant 租户归属策略(主链路 tenant_id 贯通, 2026-09-24 与 M1 联调约定)
	Tenant TenantConf
	Nacos  NacosConf // 注册/配置中心(可选)
}

// 事件未携带 tenant_id 时的处置策略取值.
const (
	// TenantPolicyDLQ 视"无归属事件"为不可重试的坏消息: 入死信台账, 待 M1 补齐后重放(默认).
	TenantPolicyDLQ = "dlq"
	// TenantPolicyZero 旧行为: 以 tenant_id=0 落库并打 WARN.
	// 仅作为 M1 改造未上线时的临时回退开关保留, 验收口径要求落库 tenant_id != 0.
	TenantPolicyZero = "zero"
)

// TenantConf 租户归属策略.
//
// 背景: M1 的两条上报通道(event-dispatcher / device-service)曾长期不带 tenant_id,
// 告警以 tenant_id=0 落库 —— 后台列表与大屏按租户查不到它, WS 广播也因
// "宁可不推也不推错园区"而跳过。现象不是报错, 而是"告警产生了却没人看得到"。
type TenantConf struct {
	// MissingPolicy 事件未携带 tenant_id(<=0)时的处置策略, 取值见 TenantPolicyDLQ / TenantPolicyZero.
	//
	// 默认 dlq(Go 零值 "" 同样按 dlq 处理 —— 与 yaml 的 default=dlq 保持同一语义):
	// 本服务不维护设备主数据, 无法反查租户, 也不允许臆造; 落 0 等于把一条真实告警
	// 变成"任何租户视图都看不见"的数据, 比拒收更难发现。入台账则可被查询/重放,
	// M1 补齐 tenant_id 后重放即可恢复, 不丢消息。
	MissingPolicy string `json:",options=dlq|zero,default=dlq"`
}

// RejectMissingTenant 返回"未携带 tenant_id 的事件是否应被拒收(入死信)而非以 0 落库".
func (t TenantConf) RejectMissingTenant() bool {
	return t.MissingPolicy != TenantPolicyZero
}

// DispatchConf M3 → M5 派单衔接配置.
//
// 为什么不硬编码: 同一条"严重"告警, 写字楼可能要求当班处理即可, 而危化品仓库要求立即到场。
// 等级描述的是告警本身的客观严重度, 优先级描述的是处置策略, 二者不应绑死 ——
// 因此允许按园区/部署在 yaml 里逐等级调整, 缺省使用 dispatch.DefaultTable.
type DispatchConf struct {
	// LevelPriority 覆盖默认映射: key 为告警等级("1"提示 / "2"一般 / "3"严重 / "4"紧急),
	// value 为 M5 工单优先级(1紧急 / 2高 / 3普通)。
	//
	// 非法条目(等级越界 / 优先级不在 1~3)不生效, 但会在启动日志打 WARN:
	// 静默忽略会让"配置写错了"表现为"一直按默认规则派单", 现场无法分辨是没配还是配错了.
	LevelPriority map[string]int `json:",optional"`
}

// RuleConf 规则引擎开关.
type RuleConf struct {
	// DisableLegacyFallback 关闭"无启用规则时回退硬编码门禁闯入规则"的兜底(docs/m3/07 §4).
	//
	// 刻意用**负向**开关: Go 零值为 false, 即"不关闭回退"。这样直接构造 ServiceContext
	// (单测、以及任何不走 yaml 的入口)时默认仍是回退行为, 不会因为漏配一项配置就静默漏报.
	//
	// 置 true 后"规则没配/没生效"会以漏报的形式直接暴露(消费链路打 WARN),
	// 用于规则体系上线后确认配置真正生效 —— 隐式回退会让"规则不生效"
	// 长期伪装成"没有匹配事件", 排查成本极高.
	DisableLegacyFallback bool `json:",default=false"`
}

// NotifyConf 告警事件通知配置.
// 复用 Kafka.Brokers 作为 broker 来源: brokers 未配置 = 通知整体停用(启动日志留痕),
// 与设备事件消费的"缺 broker 不启动"保持一致.
type NotifyConf struct {
	// Topic 生产 topic, 留空用 notify.DefaultTopic(onepark.alarm.event).
	Topic string `json:",optional"`
}

// WSConf WebSocket 广播配置(docs/m3/09 §4 方案B: Redis Pub/Sub).
type WSConf struct {
	// BroadcastChannel 跨实例广播使用的 Redis Pub/Sub 频道名.
	// 留空 = 关闭跨实例广播, Hub 退化为单实例内存广播(本地开发可不配);
	// 多副本部署必须配置 —— 否则产生告警的实例无法推送到连接在其他实例上的大屏.
	BroadcastChannel string `json:",optional"`

	// AuthSecret WS 握手鉴权密钥, 必须与 auth-service 的 JWT_SECRET / 网关 AUTH_SECRET 一致.
	//
	// 留空 = **不启用鉴权**, 退回 query ?tenant_id= 的既有行为(仅内网/网关后部署可接受);
	// 启用后握手必须携带有效 access token, **租户一律取自令牌**, 不再信任 query。
	//
	// 用"密钥非空即启用"而不是布尔开关: 少一个开关就少一种"开关开了但密钥没配"的
	// 半启用状态; 且启动日志会按启用与否分别留痕, 未启用不会被误认为已启用。
	AuthSecret string `json:",optional"`

	// AllowedOrigins Origin 白名单(如 ["https://ops.onepark.com"])。
	// 留空 = 不校验来源(本地联调 / 服务端客户端); 一旦配置即严格比对,
	// 非白名单来源(含不带 Origin 的请求)一律拒绝握手。
	AllowedOrigins []string `json:",optional"`
}

// KafkaConf Kafka 消费配置.
// Brokers 为逗号分隔字符串, 与 common/kafka.NewConsumer 签名一致.
type KafkaConf struct {
	Brokers string `json:",optional"`
	GroupID string `json:",default=alarm-service"`
}

// ESConf Elasticsearch 配置(历史告警检索 #41 / 告警双写 #44).
// Addresses 为空(或环境变量未注入, 仍为 ${VAR} 字面量)时视为未配置 ES,
// 检索自动降级 MySQL, 不阻断启动.
type ESConf struct {
	Addresses []string `json:",optional"`
	Username  string   `json:",optional"`
	Password  string   `json:",optional"`
	// Index 历史告警索引名, 留空用 search.DefaultIndex(alarm_history).
	Index string `json:",optional"`
	// Analyzer content 字段的分词器; 留空用 ES 默认分词器(standard).
	//
	// 中文场景应填 ik_max_word(索引)/ik_smart(检索), 但**必须 ES 已装 ik 插件**:
	// compose 起的是官方镜像(无插件), 填了会让建索引失败 —— 此时 search.EnsureIndex
	// 会自动降级为默认分词器并记 WARN, 不会让检索整体不可用。
	// 未装插件时留空即可: 关键词检索走 LIKE/默认分词仍可用, 只是中文召回粒度是单字。
	Analyzer string `json:",optional"`
}

// NacosConf 注册/配置中心 (可选).
type NacosConf struct {
	Address string `json:",optional"`
}
