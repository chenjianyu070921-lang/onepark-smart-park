// Package mq 消费 M1 上报到 Kafka 的设备遥测数据, 清洗后写进 energy_reading 表(接口55)
package mq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/energy-data-service/internal/model"
)

// Telemetry 设备遥测消息: 这是给 M1 同学的对接契约, 他往 Kafka 里发的数据必须是这个格式
//
//	{"device_id":"METER-A01","zone_id":"A栋","energy_kwh":12345.6,"power_kw":1.2,"reported_at":"2026-09-15 10:00:00"}
type Telemetry struct {
	DeviceID   string   `json:"device_id"`   // 设备编号, 必填
	ZoneID     string   `json:"zone_id"`     // 所属区域, 必填
	EnergyKwh  float64  `json:"energy_kwh"`  // 电表累计读数, 必填
	PowerKw    *float64 `json:"power_kw"`    // 瞬时功率, 可选
	ReportedAt string   `json:"reported_at"` // 采集时间, 格式 2026-09-15 10:00:00, 必填
}

// toReading 把消息转成数据库记录, 顺带做数据清洗
// 返回 false 表示这是条脏数据, 应该丢掉不入库
func (t Telemetry) toReading() (*model.EnergyReading, bool) {
	if strings.TrimSpace(t.DeviceID) == "" {
		return nil, false // 没有设备号, 不知道是谁的数据
	}
	if t.EnergyKwh < 0 {
		return nil, false // 电表读数不可能是负数
	}
	reportedAt, err := parseReportedAt(t.ReportedAt)
	if err != nil {
		return nil, false // 时间解析不了, 这条没法按时间排序
	}
	return &model.EnergyReading{
		DeviceID:   t.DeviceID,
		ZoneID:     t.ZoneID,
		EnergyKwh:  t.EnergyKwh,
		PowerKw:    t.PowerKw,
		ReportedAt: reportedAt,
	}, true
}

func parseReportedAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty reported_at")
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("bad time format: " + s)
}

const (
	batchSize      = 50              // 攒够 50 条写一次库
	flushInterval  = 2 * time.Second // 或者最多等 2 秒
	layoutReported = "2006-01-02 15:04:05"
)

// Consumer Kafka 消费者
type Consumer struct {
	reader *kafka.Reader
	model  *model.EnergyReadingModel
	topic  string
	cancel context.CancelFunc
}

// NewConsumer 创建消费者, 连不上 Kafka 也不会让服务崩, 只是记日志
func NewConsumer(brokers []string, topic, group string, m *model.EnergyReadingModel) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     group,
		StartOffset: kafka.LastOffset, // 只消费启动之后的新消息
		MinBytes:    1,
		MaxBytes:    10 << 20, // 10MB
	})
	return &Consumer{reader: reader, model: m, topic: topic}
}

// Start 启动消费循环(实现 go-zero 的 service.Service 接口, 由 ServiceGroup 统一拉起)
func (c *Consumer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.loop(ctx)
	logx.Infof("kafka consumer started, topic=%s", c.topic)
}

// Stop 停止消费
func (c *Consumer) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if err := c.reader.Close(); err != nil {
		logx.Errorf("kafka reader close error: %v", err)
	}
}

func (c *Consumer) loop(ctx context.Context) {
	var batch []*model.EnergyReading
	var msgs []kafka.Message

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := c.model.BatchInsert(ctx, batch); err != nil {
			logx.Errorf("批量写入 energy_reading 失败, 条数=%d err=%v", len(batch), err)
			return // 写失败就不提交 offset, 下次还能再消费到
		}
		if err := c.reader.CommitMessages(ctx, msgs...); err != nil {
			logx.Errorf("提交 kafka offset 失败: %v", err)
		}
		logx.Infof("已入库 %d 条遥测数据", len(batch))
		batch = batch[:0]
		msgs = msgs[:0]
	}

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		// 先看有没有退出信号 / 到没到刷盘时间(不能阻塞在读消息上, 否则定时器永远轮不到)
		select {
		case <-ctx.Done():
			flush()
			return
		case <-ticker.C:
			flush()
		default:
		}

		// 最多等 500ms, 没消息就回到上面让定时器有机会触发
		readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		msg, err := c.reader.FetchMessage(readCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				flush()
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue // 只是这一轮没消息
			}
			// Kafka 连不上时别空转把 CPU 跑满, 歇一秒再试
			time.Sleep(time.Second)
			continue
		}

		var t Telemetry
		if err := json.Unmarshal(msg.Value, &t); err != nil {
			logx.Errorf("消息不是合法 JSON, 丢弃: %s", string(msg.Value))
			_ = c.reader.CommitMessages(ctx, msg) // 坏消息没必要重试
			continue
		}
		r, ok := t.toReading()
		if !ok {
			logx.Errorf("脏数据, 丢弃: %+v", t)
			_ = c.reader.CommitMessages(ctx, msg)
			continue
		}
		batch = append(batch, r)
		msgs = append(msgs, msg)
		if len(batch) >= batchSize {
			flush()
		}
	}
}
