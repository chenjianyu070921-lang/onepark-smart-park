package kafka

import (
	"context"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"
)

// 消费失败处理策略:
//  1. handler 失败按指数退避重试(500ms 起, 每次翻倍, 10s 封顶), 应对 DB 等临时故障;
//  2. 重试超过 maxHandlerRetries 仍失败, 视为毒消息 —— 记录关键信息后提交位移跳过,
//     避免单条坏消息无限紧密重试打爆下游、卡死整个分区;
//  3. 成功后重试计数归零.
const (
	maxHandlerRetries = 5
	baseBackoff       = 500 * time.Millisecond
	maxBackoff        = 10 * time.Second
)

// Producer Kafka 生产者封装, 复用一个 Writer 批量写入.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer 根据 broker 列表创建生产者.
// brokers 形如 "kafka1:9092,kafka2:9092", 以逗号分隔.
func NewProducer(brokers string) *Producer {
	return &Producer{
		writer: &kafka.Writer{
			Addr:         kafka.TCP(strings.Split(brokers, ",")...),
			Balancer:     &kafka.LeastBytes{}, // 按分区负载均衡
			RequiredAcks: kafka.RequireAll,    // 需所有 ISR 确认, 保证不丢
		},
	}
}

// Publish 向指定 topic 发送一条消息, key 用于分区路由(如车牌号/设备ID).
func (p *Producer) Publish(ctx context.Context, topic string, key, value []byte) error {
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   key,
		Value: value,
	})
}

// Close 释放 Writer 资源.
func (p *Producer) Close() error {
	return p.writer.Close()
}

// Consumer Kafka 消费者封装, 基于消费者组实现水平扩展.
type Consumer struct {
	reader *kafka.Reader
}

// NewConsumer 根据 broker 列表、topic、group 创建消费者.
func NewConsumer(brokers, topic, group string) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:        strings.Split(brokers, ","),
			Topic:          topic,
			GroupID:        group,
			StartOffset:    kafka.FirstOffset, // 首次从最早未消费位移开始
			CommitInterval: 0,                 // 每消息手动提交(在 handler 成功后)
		}),
	}
}

// Consume 启动消费循环, 每条消息交给 handler 处理; handler 返回 nil 才提交位移.
// 失败消息先退避重试(见 maxHandlerRetries), 超限后记录并跳过, 防止分区被毒消息卡死.
// 阻塞运行, 调用方应在独立 goroutine 中执行; ctx 取消即退出.
func (c *Consumer) Consume(ctx context.Context, handler func(ctx context.Context, msg kafka.Message) error) error {
	var retries int
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			// ctx 取消或连接断开, 退出循环.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if hErr := handler(ctx, m); hErr != nil {
			retries++
			if retries >= maxHandlerRetries {
				// 毒消息隔离: 超限后提交位移跳过, 必须留足排障信息(内容截断防日志爆炸).
				logx.WithContext(ctx).Errorf(
					"[kafka] 消息重试 %d 次仍失败, 跳过并提交位移: topic=%s partition=%d offset=%d, err=%v, value=%.512s",
					retries, m.Topic, m.Partition, m.Offset, hErr, m.Value)
				retries = 0
			} else {
				if !sleepBackoff(ctx, retries) {
					return ctx.Err()
				}
				continue
			}
		} else {
			retries = 0
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			return err
		}
	}
}

// sleepBackoff 按重试次数指数退避(500ms 起, 翻倍, 10s 封顶); ctx 取消返回 false.
func sleepBackoff(ctx context.Context, retries int) bool {
	d := baseBackoff << (retries - 1)
	if d > maxBackoff {
		d = maxBackoff
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Close 释放 Reader 资源.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
