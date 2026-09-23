package config

import (
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"onepark/common/gormx"
	"onepark/common/minio"
	"onepark/common/redisx"
)

// Config 定义 workorder-service 的运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis、Kafka 等连接信息.
type Config struct {
	rest.RestConf
	Rpc   zrpc.RpcServerConf // 工单 gRPC 服务(M5 大屏 ListWorkOrders 聚合查询, 监听 9091)
	MySQL gormx.MySQLConf  // MySQL 连接配置
	Redis redisx.RedisConf // Redis 连接配置(分布式锁防重)
	Kafka KafkaConf        // Kafka 连接配置
	MinIO miniox.MinIOConf // MinIO 对象存储(工单附件上传)

	// EscalationCron 工单超时升级定时任务(复用 leasing 的 Redis 锁防重模式).
	EscalationCron EscalationCronConf
}

// KafkaConf 同时服务两个方向:
//   - 生产: 建单/派单/状态流转发 workorder-event(Brokers);
//   - 消费: 消费 alarm-event 自动建报修工单(Topic/Group/Enabled).
//
// ⚠️ 本项目使用的是**多项目共用的共享 broker**, 因此有两道硬约束:
//  1. Group 必须形如 m2-{service}-{env}-{owner}。Kafka 的位移按消费组提交,
//     若与队友或其它项目重名, 会把别人该消费的消息"吃掉"(标记为已消费)。
//  2. Enabled 默认 false, 必须显式开启。消费者会在消费到告警时**自动建真实工单**,
//     在共享 broker 上误开会产生脏数据。
type KafkaConf struct {
	Brokers string `json:",optional"`                 // 多个 broker 用逗号分隔, 如 kafka1:9092,kafka2:9092
	Topic   string `json:",default=alarm-event"`      // 消费的告警主题(M1 event-dispatcher 投递)
	Group   string `json:",default=m2-workorder-dev"` // 消费组(项目-服务-环境, 严禁与他人重名)
	Enabled bool   `json:",default=false"`            // 告警自动建单消费者开关, 默认关闭

	// DefaultTenantId 告警消息未携带 tenant_id 时, 自动建单落到哪个园区.
	// 告警事件(event-dispatcher→alarm-event)当前不强制携带租户, 需要部署时指定兜底值.
	DefaultTenantId int64 `json:",default=1"`
}

// EscalationCronConf 工单超时升级配置.
// 把"超过 PendingTimeoutHours 小时仍处于活跃态且非紧急"的工单自动提升为紧急优先级,
// 并写一条 system 升级流水, 便于后续 SLA 考核与催办.
type EscalationCronConf struct {
	Enabled             bool   `json:",default=false"` // 默认关闭, 部署时显式开启
	Spec                string `json:",optional"`      // 扫描周期(标准 5 位 cron), 由 yaml 显式指定(默认每 30 分钟)
	PendingTimeoutHours int64  `json:",default=24"`    // 活跃态超过该小时数未处理即升级为紧急
}
