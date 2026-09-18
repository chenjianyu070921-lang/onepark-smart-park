package svc

import (
	"context"
	"log"
	"strings"
	"time"

	"onepark/app/parking-service/internal/config"
	"onepark/common/dedup"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
)

// ServiceContext 持有 parking-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接、Redis 客户端与 Kafka 生产者, 供 logic 层使用.
type ServiceContext struct {
	Config   config.Config
	DB       *gormx.DB       // GORM MySQL 连接
	Redis    *redisx.Client  // Redis 客户端
	Producer *kafka.Producer // Kafka 生产者(发布停车/告警事件)
	// Dedup 消费幂等去重(L1); nil 表示未配置 Redis, 由 L3 唯一索引兜底.
	Dedup dedup.Deduper
}

// parkingDedupTTL 消费幂等键有效期(24h), 覆盖 Kafka 重投与人工重放的周期.
const parkingDedupTTL = 24 * time.Hour

// NewServiceContext 根据配置初始化全局依赖.
// 若配置了 MySQL DSN 但初始化失败, 直接退出进程, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] parking-service mysql data source is empty, db not initialized")
	}

	var producer *kafka.Producer
	if c.Kafka.Brokers != "" {
		producer = kafka.NewProducer(c.Kafka.Brokers)
	} else {
		log.Printf("[warn] parking-service kafka brokers empty, producer not initialized")
	}

	svcCtx := &ServiceContext{
		Config:   c,
		DB:       db,
		Redis:    redisx.NewClient(&c.Redis),
		Producer: producer,
	}
	// 幂等去重: Redis 未配置时留 nil, 消费链路退化为只靠唯一索引兜底(并在启动日志留痕).
	if strings.TrimSpace(c.Redis.Addr) != "" && !strings.Contains(c.Redis.Addr, "${") {
		svcCtx.Dedup = dedup.NewRedisDeduper(svcCtx.Redis, parkingDedupTTL)
	} else {
		log.Printf("[warn] parking-service redis addr empty, consume dedup falls back to mysql unique key only")
	}
	return svcCtx
}

// StartConsumers 启动后台 Kafka 消费者(地磁遥测 -> 停车记录), 独立 goroutine 运行.
// 仅在配置了 Kafka 时启动; 消费失败不影响主服务.
// 带断线重连: broker 瞬时故障/重启导致 Consume 返回错误时, 间隔 3 秒自动重建消费者继续消费,
// 避免一次网络抖动造成消费永久停止(共享 broker 不稳定场景下的必要兜底); 服务主动退出(ctx 取消)则不再重试.
func (s *ServiceContext) StartConsumers(ctx context.Context) {
	if s.Config.Kafka.Brokers == "" {
		log.Printf("[warn] parking-service kafka brokers empty, skip consumers")
		return
	}
	go func() {
		for {
			consumer := kafka.NewConsumer(s.Config.Kafka.Brokers, kafka.TopicDeviceTelemetry, kafka.GroupParking)
			log.Printf("[info] parking-service start consume topic=%s", kafka.TopicDeviceTelemetry)
			err := consumer.Consume(ctx, s.handleTelemetry)
			consumer.Close() // 每轮重建前释放旧 reader, 防止连接泄漏
			// 服务退出(ctx 取消)或 handler 返回的错误, 均不再重试.
			if ctx.Err() != nil {
				if err != nil {
					log.Printf("[error] parking telemetry consumer exited: %v", err)
				}
				return
			}
			log.Printf("[warn] parking telemetry consumer exited: %v, reconnecting in 3s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()
}
