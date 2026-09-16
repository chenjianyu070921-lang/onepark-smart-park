package config

import (
	"github.com/zeromicro/go-zero/rest"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 dispatch-service 的运行配置.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // MySQL 连接配置(dispatch_db)
	Redis redisx.RedisConf // Redis 连接配置
	Kafka KafkaConf        // 告警事件消费配置

	// JwtSecret HTTP 接口鉴权密钥; 为空时放行。
	// 填上后审计流水的 operator_id 才有值(来自 token 的 userId claim), 否则恒为 0。
	JwtSecret string `json:",env=JWT_SECRET,optional"`
}

// KafkaConf 告警事件消费配置(接口清单 #74).
//
// ⚠️ 本项目使用的是**多项目共用的共享 broker**, 因此有两道硬约束:
//
//  1. Group 必须形如 m5-{service}-{env}-{owner}。Kafka 的位移按消费组提交,
//     若与队友或其它项目重名, 会把别人该消费的消息"吃掉"(标记为已消费)。
//
//  2. Enabled 默认 false, 必须显式开启。#74 会在消费到告警时**自动建单**,
//     在共享 broker 上误开会产生脏数据。
type KafkaConf struct {
	Brokers string `json:",optional"`
	Topic   string `json:",default=alarm-event"`
	Group   string `json:",default=m5-dispatch-dev"`
	Enabled bool   `json:",default=false"`
	// DefaultTenantId 告警消息体不携带租户信息, 自动建单时落到该园区(RBAC 隔离维度).
	// 默认 1 与 l2_tenant_id_migration.sql 的"默认园区"回填口径一致.
	DefaultTenantId int64 `json:",default=1"`
}
