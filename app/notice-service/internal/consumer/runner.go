package consumer

import (
	"context"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/notice-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

// WorkorderRunner 站内通知消费者运行器: 装配 consumer+handler, 在独立 goroutine 常驻消费.
type WorkorderRunner struct {
	logx.Logger
	handler *WorkorderHandler
	topic   string // 消费的工单事件主题, 供启动日志展示
	group   string // 消费组, 断线重建消费者时复用
	enabled bool   // 配置开关: 共享 broker 上误开会真实写库, 默认关闭
	brokers string // broker 列表, 断线重建消费者时复用
}

// NewWorkorderRunner 根据配置装配消费者; Enabled=false 时返回空跑实例(Start 直接跳过).
func NewWorkorderRunner(cfg config.KafkaConf, db *gormx.DB) *WorkorderRunner {
	return &WorkorderRunner{
		Logger:  logx.WithContext(context.Background()),
		handler: NewWorkorderHandler(db),
		topic:   cfg.Topic,
		group:   cfg.Group,
		enabled: cfg.Enabled,
		brokers: cfg.Brokers,
	}
}

// Start 启动消费循环(阻塞), 必须在独立 goroutine 中调用; ctx 取消即退出.
// 带断线重连(与 parking 消费端同范式): broker 瞬时故障/重启导致 Consume 返回错误时,
// 间隔 3 秒重建消费者继续消费; 避免一次网络抖动让站内通知永久停摆.
func (r *WorkorderRunner) Start(ctx context.Context) {
	if !r.enabled {
		r.Infof("[consumer] 站内通知消费者未启用(Enabled=false), 跳过消费")
		return
	}
	if r.brokers == "" {
		r.Infof("[consumer] 站内通知消费者未启用(KAFKA_BROKERS 未配置), 跳过消费")
		return
	}
	r.Infof("[consumer] 站内通知消费者启动: topic=%s", r.topic)

	for {
		consumer := kafka.NewConsumer(r.brokers, r.topic, r.group)
		// Consume 阻塞循环: handler 返回 nil 才提交位移, 返回 error 保留消息等待重试.
		// 坏消息由 handler 内部"记录后跳过", 不会卡死分区.
		err := consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
			return r.handler.Handle(ctx, msg.Value)
		})
		_ = consumer.Close() // 每轮重建前释放旧 reader, 防止连接泄漏
		if ctx.Err() != nil {
			return
		}
		r.Errorf("[consumer] 站内通知消费循环异常退出: %v, 3s 后重建消费者", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
