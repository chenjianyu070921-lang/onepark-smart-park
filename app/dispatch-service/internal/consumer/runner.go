package consumer

import (
	"context"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dispatch-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

// Runner 把 common/kafka 的消费者与告警建单处理器串起来。
//
// 复用 common/kafka 而非自建 Reader: 它已经实现了「handler 成功才提交位移」的语义,
// 失败消息不提交、等待重试, 正是本场景需要的。
type Runner struct {
	logx.Logger
	consumer *kafka.Consumer
	handler  *AlarmHandler
	topic    string
	group    string
}

// NewRunner 按配置构造消费者。
// 未启用、或未配置 broker 时返回 nil, 调用方据此跳过启动。
// 「默认关闭」是刻意设计: 共享 broker 上误开消费者会自动建出真实工单。
func NewRunner(c config.Config, db *gormx.DB) *Runner {
	if !c.Kafka.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Kafka.Brokers) == "" {
		logx.Error("[consumer] Kafka.Enabled=true 但 Brokers 为空, 消费者不启动")
		return nil
	}
	return &Runner{
		Logger:   logx.WithContext(context.Background()),
		consumer: kafka.NewConsumer(c.Kafka.Brokers, c.Kafka.Topic, c.Kafka.Group),
		handler:  NewAlarmHandler(db),
		topic:    c.Kafka.Topic,
		group:    c.Kafka.Group,
	}
}

// Start 阻塞运行消费循环, 应在独立 goroutine 中调用; ctx 取消即退出。
func (r *Runner) Start(ctx context.Context) {
	r.Infof("[consumer] 开始消费告警事件: topic=%s, group=%s", r.topic, r.group)

	// 用 common/kafka.Message(它是 segmentio kafka.Message 的别名)而不是直接依赖
	// segmentio 包 —— 这样本服务不必把 kafka 客户端库列为直接依赖, 也避免了包重名的别名。
	err := r.consumer.Consume(ctx, func(ctx context.Context, msg kafka.Message) error {
		return r.handler.Handle(ctx, msg.Value)
	})
	if err != nil && ctx.Err() == nil {
		r.Errorf("[consumer] 消费循环异常退出: topic=%s, err=%v", r.topic, err)
	}

	r.Infof("[consumer] 消费循环已停止: topic=%s", r.topic)
}

// Close 释放消费者资源。
func (r *Runner) Close() error {
	return r.consumer.Close()
}
