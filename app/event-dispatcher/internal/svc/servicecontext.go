package svc

import (
	"fmt"
	"os"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/event-dispatcher/internal/archive"
	"onepark/app/event-dispatcher/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

type ServiceContext struct {
	Config   config.Config
	Producer *kafka.Producer
	Resolver *archive.Resolver
}

// NewServiceContext 初始化上下文.
// 约定: yaml 中只写 ${XXX} 占位, 此处统一展开环境变量并填充默认值,
// 避免 conf.MustLoad 未开启 env 时把占位符当成真实地址连接.
func NewServiceContext(c config.Config) *ServiceContext {
	c.EMQXBroker = os.ExpandEnv(c.EMQXBroker)
	if c.EMQXBroker == "" {
		c.EMQXBroker = "tcp://localhost:1883"
	}
	c.EMQXClientId = os.ExpandEnv(c.EMQXClientId)
	if c.EMQXClientId == "" {
		c.EMQXClientId = "event-dispatcher"
	}
	c.EMQXUsername = os.ExpandEnv(c.EMQXUsername)
	c.EMQXPassword = os.ExpandEnv(c.EMQXPassword)
	c.EMQXTopic = os.ExpandEnv(c.EMQXTopic)
	if c.EMQXTopic == "" {
		c.EMQXTopic = "onepark/device/#"
	}
	c.KafkaBrokers = os.ExpandEnv(c.KafkaBrokers)
	if c.KafkaBrokers == "" {
		c.KafkaBrokers = "localhost:9092"
	}

	// 设备档案只读连接: DSN 缺失时按 ArchiveRequired 决定快速失败或显式降级.
	resolver, err := buildResolver(c)
	if err != nil {
		logx.Must(err)
	}
	if resolver == nil {
		logx.Severef("[WARN] 设备档案库未配置(DISPATCHER_MYSQL_DSN 为空)且 ArchiveRequired=false: " +
			"消息将以 tenant_id=0 零值放行, M3 告警按租户不可见, 仅限本地调试")
	}

	return &ServiceContext{
		Config:   c,
		Producer: kafka.NewProducer(c.KafkaBrokers),
		Resolver: resolver,
	}
}

// buildResolver 依据配置构造档案解析器; DSN 为空时按 ArchiveRequired 决定
// 返回错误(fail-closed)或 nil resolver(显式降级, 调用方需告警).
func buildResolver(c config.Config) (*archive.Resolver, error) {
	dsn := os.ExpandEnv(c.MySQLDSN)
	if dsn == "" {
		if c.ArchiveRequired {
			return nil, fmt.Errorf("MySQLDSN 未配置且 ArchiveRequired=true: 拒绝启动, " +
				"零值放行会导致 tenant_id=0 消息污染下游告警/计费; 本地调试可设 ARCHIVE_REQUIRED=false")
		}
		return nil, nil
	}
	db, err := gormx.NewDB(dsn)
	if err != nil {
		return nil, fmt.Errorf("初始化 MySQL 失败: %w", err)
	}
	return archive.NewResolver(archive.NewGormReader(db), 30*time.Second), nil
}
