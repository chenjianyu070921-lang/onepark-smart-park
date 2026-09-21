package consumer

import (
	"context"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/workorder-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

// AlarmRunner 告警建单消费者运行器: 装配 consumer+handler, 在独立 goroutine 常驻消费.
type AlarmRunner struct {
	logx.Logger
	handler         *AlarmHandler
	topic           string // 消费的告警主题, 供启动日志展示
	group           string // 消费组, 断线重建消费者时复用
	brokers         string // broker 列表, 断线重建消费者时复用
	enabled         bool   // 配置开关: 共享 broker 上误开会产生脏数据, 默认关闭
	defaultTenantID int64  // 告警未携带租户时的建单兜底园区
}

// NewAlarmRunner 根据配置装配告警消费者; Enabled=false 时返回空跑实例(Start 直接跳过).
func NewAlarmRunner(cfg config.KafkaConf, db *gormx.DB, producer *kafka.Producer) *AlarmRunner {
	return &AlarmRunner{
		Logger:          logx.WithContext(context.Background()),
		handler:         NewAlarmHandler(db, producer),
		topic:           cfg.Topic,
		group:           cfg.Group,
		brokers:         cfg.Brokers,
		enabled:         cfg.Enabled,
		defaultTenantID: cfg.DefaultTenantId,
	}
}

// Start 启动消费循环(阻塞), 必须在独立 goroutine 中调用; ctx 取消即退出.
// 带断线重连(与 parking 消费端同范式): broker 瞬时故障/重启导致 Consume 返回错误时,
// 间隔 3 秒重建消费者继续消费; 避免一次网络抖动让告警自动建单永久停摆.
func (r *AlarmRunner) Start(ctx context.Context) {
	if !r.enabled {
		r.Infof("[consumer] 告警自动建单未启用(Enabled=false), 跳过消费")
		return
	}
	if r.brokers == "" {
		r.Infof("[consumer] 告警自动建单未启用(KAFKA_BROKERS 未配置), 跳过消费")
		return
	}
	r.Infof("[consumer] 告警自动建单消费者启动: topic=%s", r.topic)

	for {
		consumer := kafka.NewConsumer(r.brokers, r.topic, r.group)
		// Consume 阻塞循环: handler 返回 nil 才提交位移, 返回 error 保留消息等待重试.
		// 坏消息由 handler 内部"记录后跳过", 不会卡死分区.
		err := consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
			return r.handler.Handle(ctx, msg.Value, r.defaultTenantID)
		})
		_ = consumer.Close() // 每轮重建前释放旧 reader, 防止连接泄漏
		if ctx.Err() != nil {
			return
		}
		r.Errorf("[consumer] 告警消费循环异常退出: %v, 3s 后重建消费者", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
