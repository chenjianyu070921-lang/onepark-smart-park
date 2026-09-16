package consumer

import (
	"context"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/notice-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

// WorkorderRunner 站内通知消费者运行器: 装配 consumer+handler, 在独立 goroutine 常驻消费.
type WorkorderRunner struct {
	logx.Logger
	consumer *kafka.Consumer
	handler  *WorkorderHandler
	topic    string // 消费的工单事件主题, 供启动日志展示
	enabled  bool   // 配置开关: 共享 broker 上误开会真实写库, 默认关闭
}

// NewWorkorderRunner 根据配置装配消费者; Enabled=false 时返回空跑实例(Start 直接跳过).
func NewWorkorderRunner(cfg config.KafkaConf, db *gormx.DB) *WorkorderRunner {
	return &WorkorderRunner{
		Logger:   logx.WithContext(context.Background()),
		consumer: kafka.NewConsumer(cfg.Brokers, cfg.Topic, cfg.Group),
		handler:  NewWorkorderHandler(db),
		topic:    cfg.Topic,
		enabled:  cfg.Enabled,
	}
}

// Start 启动消费循环(阻塞), 必须在独立 goroutine 中调用; ctx 取消即退出.
func (r *WorkorderRunner) Start(ctx context.Context) {
	if !r.enabled {
		r.Infof("[consumer] 站内通知消费者未启用(Enabled=false), 跳过消费")
		return
	}
	r.Infof("[consumer] 站内通知消费者启动: topic=%s", r.topic)

	// 优雅退出: ctx 取消时关闭 reader, Consume 循环随之结束.
	go func() {
		<-ctx.Done()
		_ = r.consumer.Close()
	}()

	// Consume 阻塞循环: handler 返回 nil 才提交位移, 返回 error 保留消息等待重试.
	// 坏消息由 handler 内部"记录后跳过", 不会卡死分区.
	if err := r.consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
		return r.handler.Handle(ctx, msg.Value)
	}); err != nil && ctx.Err() == nil {
		r.Errorf("[consumer] 站内通知消费循环异常退出: %v", err)
	}
}
