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
	Rule   RuleConf         // 规则引擎相关开关
	Nacos  NacosConf        // 注册/配置中心(可选)
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
