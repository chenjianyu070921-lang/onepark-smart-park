package kafka

import (
	"context"
	"strings"

	"github.com/segmentio/kafka-go"
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
			CommitInterval: 0,                  // 每消息手动提交(在 handler 成功后)
		}),
	}
}

// Consume 启动消费循环, 每条消息交给 handler 处理; handler 返回 nil 才提交位移.
// 阻塞运行, 调用方应在独立 goroutine 中执行; ctx 取消即退出.
func (c *Consumer) Consume(ctx context.Context, handler func(ctx context.Context, msg kafka.Message) error) error {
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
			// 处理失败不提交, 等待后续重试(至多一次语义的简单兜底).
			continue
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			return err
		}
	}
}

// Close 释放 Reader 资源.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
