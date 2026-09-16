package consumer

import (
	"context"
	"strings"
	"time"

	segkafka "github.com/segmentio/kafka-go"
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
	brokers  string
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
		handler:  NewAlarmHandler(db, c.Kafka.DefaultTenantId),
		brokers:  c.Kafka.Brokers,
		topic:    c.Kafka.Topic,
		group:    c.Kafka.Group,
	}
}

// reconnectDelay 消费循环异常退出后的重连间隔。
const reconnectDelay = 5 * time.Second

// Start 阻塞运行消费循环, 应在独立 goroutine 中调用; ctx 取消即退出。
// 循环异常退出(如连接断开)时自动重建 Reader 重连, 避免进程存活但消费静默停止。
func (r *Runner) Start(ctx context.Context) {
	r.Infof("[consumer] 开始消费告警事件: topic=%s, group=%s", r.topic, r.group)

	for {
		err := r.consumer.Consume(ctx, func(ctx context.Context, msg segkafka.Message) error {
			return r.handler.Handle(ctx, msg.Value)
		})
		// 正常退出(ctx 取消)即结束; 异常退出则延迟后重连。
		if ctx.Err() != nil {
			break
		}
		r.Errorf("[consumer] 消费循环异常退出, %s 后重连: topic=%s, err=%v", reconnectDelay, r.topic, err)

		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
			r.consumer = kafka.NewConsumer(r.brokers, r.topic, r.group)
		}
	}

	r.Infof("[consumer] 消费循环已停止: topic=%s", r.topic)
}

// Close 释放消费者资源。
func (r *Runner) Close() error {
	return r.consumer.Close()
}
