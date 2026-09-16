package svc

import (
	"context"
	"log"
	"strings"

	"onepark/app/alarm-service/internal/config"
	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
)

// dedupTTL 消息级幂等键有效期(L1), 与 docs/m3/06 §4 一致.
const dedupTTL = 24 * 60 * 60 * 1e9 // 24h, 单位纳秒(time.Duration)

// ServiceContext 持有 alarm-service 运行时的全局依赖.
// Alarms/Dedup 抽成接口是为了让消费链路脱离 MySQL/Redis 可单测(见 internal/svc/consumer_test.go).
type ServiceContext struct {
	Config config.Config
	DB     *gormx.DB        // GORM MySQL 连接(alarm_db)
	Redis  *redisx.Client   // Redis 客户端(幂等去重 / 冷却窗口)
	Alarms model.AlarmModel // 告警数据访问层
	Dedup  dedup.Deduper    // 消息级幂等去重(L1)
}

// NewServiceContext 根据配置初始化全局依赖.
// MySQL DSN 未配置时不初始化 DB(本地无中间件仍可启动); 已配置但连接串非法则直接退出, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if dsn := unresolvedToEmpty(c.MySQL.DataSource); dsn != "" {
		var err error
		db, err = gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		log.Printf("[info] alarm-service mysql initialized, db=%s", databaseOf(dsn))
	} else {
		log.Printf("[warn] alarm-service mysql data source is empty, db not initialized")
	}

	rds := redisx.NewClient(&c.Redis)

	svcCtx := &ServiceContext{
		Config: c,
		DB:     db,
		Redis:  rds,
	}
	if db != nil {
		svcCtx.Alarms = model.NewAlarmModel(db)
		svcCtx.Dedup = dedup.NewRedisDeduper(rds, dedupTTL)
	}
	return svcCtx
}

// StartConsumers 启动后台 Kafka 消费者(设备遥测 -> 安防告警), 独立 goroutine 运行.
// 仅在 MySQL 与 Kafka 均配置时启动: 缺 DB 无法落库, 缺 broker 无法消费, 二者缺一宁不启动也不降级丢告警.
func (s *ServiceContext) StartConsumers(ctx context.Context) {
	if s.Alarms == nil {
		log.Printf("[warn] alarm-service mysql not ready, skip device event consumer")
		return
	}
	brokers := unresolvedToEmpty(s.Config.Kafka.Brokers)
	if brokers == "" {
		log.Printf("[warn] alarm-service kafka brokers empty, skip device event consumer")
		return
	}
	go func() {
		consumer := kafka.NewConsumer(brokers, kafka.TopicDeviceTelemetry, s.Config.Kafka.GroupID)
		defer func() {
			if err := consumer.Close(); err != nil {
				log.Printf("[error] alarm-service close kafka consumer failed: %v", err)
			}
		}()
		log.Printf("[info] alarm-service start consume topic=%s group=%s", kafka.TopicDeviceTelemetry, s.Config.Kafka.GroupID)
		if err := consumer.Consume(ctx, s.HandleDeviceEvent); err != nil && ctx.Err() == nil {
			log.Printf("[error] alarm device event consumer exited: %v", err)
		}
	}()
}

// unresolvedToEmpty 将未展开的环境变量占位符视为空值.
// go-zero 在环境变量缺失时会保留 ${VAR} 字面量, 直接拿去连 MySQL 会报 "invalid DSN".
func unresolvedToEmpty(v string) string {
	if strings.Contains(v, "${") {
		return ""
	}
	return v
}

// databaseOf 从 GORM DSN 中截取数据库名, 仅用于启动日志, 不建立连接.
func databaseOf(dsn string) string {
	// DSN 形如 user:pass@tcp(host:port)/dbname?charset=utf8mb4
	i := strings.LastIndex(dsn, "/")
	if i < 0 || i == len(dsn)-1 {
		return "<unknown>"
	}
	name := dsn[i+1:]
	if j := strings.IndexAny(name, "?&"); j >= 0 {
		name = name[:j]
	}
	return name
}
