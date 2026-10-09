// Package mq 消费 M1 上报到 Kafka 的设备遥测数据, 清洗后写进 energy_reading 表(接口55)
//
// 对接说明(重要): topic 里跑的不只是电表数据。M1 是物联接入管道,
// 地磁、门禁、烟感这些事件全都往同一个 topic 里发, 所以这边要做两件事:
//  1. 按 M1 已经定好的信封格式解析(不能自己发明消息结构)
//  2. 只挑出 payload 里带 energy_kwh 的当能耗数据处理, 其余静默跳过
package mq

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/energy-data-service/internal/model"
)

// DeviceEvent M1 投递到 Kafka 的信封格式, 与 device-service / event-dispatcher 保持一致
//
//	{"request_id":"...","device_id":"METER-A01","device_type":"meter",
//	 "event_type":"telemetry","occurred_at":1757900000,
//	 "payload":{"energy_kwh":12345.6,"power_kw":1.2},"source":"mqtt"}
//
// 注意: 能耗字段在 payload 里, 由设备侧上报, 信封上只有设备号和时间戳
type DeviceEvent struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"` // Unix 时间戳, 一般秒级
	Payload    json.RawMessage `json:"payload"`     // 设备上报的原始数据
	Source     string          `json:"source"`
}

// EnergyPayload 电表在 payload 里上报的能耗字段
// 这是 M4 和 M1(设备侧)约定的业务契约
//
//	{"energy_kwh":12345.6,"power_kw":1.2,"zone_id":"A栋"}
type EnergyPayload struct {
	EnergyKwh  *float64 `json:"energy_kwh"`  // 电表累计读数(度), 必填
	PowerKw    *float64 `json:"power_kw"`    // 瞬时功率(kW), 可选
	ZoneID     string   `json:"zone_id"`     // 区域, 可选; 不传就用 device 表的归属
	ReportedAt string   `json:"reported_at"` // 采集时间, 可选; 不传就用信封上的 occurred_at
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
	// devices 只读 M1 的 device 表, 用来补区域归属
	devices *model.DeviceModel
	topic   string
	// defaultZone 设备既没上报区域、device 表也查不到时, 归到这个区域
	defaultZone string

	// brokers/group 留一份, reader 卡死后要按原样重建(自愈)
	brokers []string
	group   string

	// zoneCache 设备号 → 区域, 查过一次就记住, 别每条消息都打一次库
	zoneMu    sync.RWMutex
	zoneCache map[string]string

	cancel context.CancelFunc
}

func (c *Consumer) newReader() *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:     c.brokers,
		Topic:       c.topic,
		GroupID:     c.group,
		StartOffset: kafka.LastOffset, // 只消费启动之后的新消息
		MinBytes:    1,
		MaxBytes:    10 << 20, // 10MB
	})
}

// NewConsumer 创建消费者, 连不上 Kafka 也不会让服务崩, 只是记日志
func NewConsumer(brokers []string, topic, group, defaultZone string,
	m *model.EnergyReadingModel, devices *model.DeviceModel) *Consumer {
	if defaultZone == "" {
		defaultZone = "未分配"
	}
	c := &Consumer{
		model:       m,
		devices:     devices,
		topic:       topic,
		defaultZone: defaultZone,
		brokers:     brokers,
		group:       group,
		zoneCache:   make(map[string]string),
	}
	c.reader = c.newReader()
	return c
}

// Start 启动消费循环(实现 go-zero 的 service.Service 接口, 由 ServiceGroup 统一拉起)
func (c *Consumer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.loop(ctx)
	logx.Infof("kafka consumer started, topic=%s, 默认区域=%s", c.topic, c.defaultZone)
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

// parse 把 M1 的消息转成数据库记录
// 返回 (记录, 是否入库, 不入库的原因); 原因为空且未入库 = 不是能耗数据, 静默跳过
func (c *Consumer) parse(ctx context.Context, raw []byte) (*model.EnergyReading, bool, string) {
	var ev DeviceEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, false, "不是合法 JSON: " + string(raw)
	}
	if strings.TrimSpace(ev.DeviceID) == "" {
		return nil, false, "缺少 device_id"
	}

	// 地磁/门禁/告警事件也走这个 topic, 它们 payload 里没有 energy_kwh,
	// 属于正常情况, 不打日志(否则日志会被刷屏), 直接跳过
	var p EnergyPayload
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &p)
	}
	if p.EnergyKwh == nil {
		return nil, false, ""
	}
	if *p.EnergyKwh < 0 {
		return nil, false, "电表读数不可能是负数"
	}

	reportedAt, ok := resolveTime(p, ev.OccurredAt)
	if !ok {
		return nil, false, "采集时间缺失"
	}

	return &model.EnergyReading{
		DeviceID:   ev.DeviceID,
		ZoneID:     c.resolveZone(ctx, ev.DeviceID, p.ZoneID),
		EnergyKwh:  *p.EnergyKwh,
		PowerKw:    p.PowerKw,
		ReportedAt: reportedAt,
	}, true, ""
}

// resolveTime 定采集时间: 设备上报的 reported_at 优先, 没有就用信封上的 occurred_at
func resolveTime(p EnergyPayload, occurredAt int64) (time.Time, bool) {
	if s := strings.TrimSpace(p.ReportedAt); s != "" {
		if t, err := parseReportedAt(s); err == nil {
			return t, true
		}
	}
	if occurredAt <= 0 {
		return time.Time{}, false
	}
	// 有些设备上报的是毫秒, 兜一下
	if occurredAt > 1e12 {
		return time.UnixMilli(occurredAt), true
	}
	return time.Unix(occurredAt, 0), true
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

// resolveZone 定区域: 设备上报的 zone_id → device 表的 building_id → 默认区域
//
// 为什么要兜底而不是直接丢: M1 消息里本来就没有区域, device 表也可能还没建档,
// 直接丢会造成"M1 一接入, 数据全没了还不知道为啥"。
// 兜底成"未分配"至少有数据, 运维看到日报里这一项有量就知道要去补归属。
func (c *Consumer) resolveZone(ctx context.Context, deviceID, fromPayload string) string {
	if z := strings.TrimSpace(fromPayload); z != "" {
		return z // 设备自己上报了, 以它为准
	}

	c.zoneMu.RLock()
	z, cached := c.zoneCache[deviceID]
	c.zoneMu.RUnlock()
	if cached {
		return z
	}

	z, err := c.devices.FindZone(ctx, deviceID)
	if err != nil {
		logx.Errorf("查设备归属失败, device=%s err=%v", deviceID, err)
	}
	if z == "" {
		// 设备没建档或没填楼栋, 归到默认区域
		z = c.defaultZone
		logx.Infof("设备 %s 未配置归属, 归入默认区域[%s]; 请在 device 表补 building_id", deviceID, z)
	}

	c.zoneMu.Lock()
	c.zoneCache[deviceID] = z
	c.zoneMu.Unlock()
	return z
}

// brokerAlive 轻量探测: broker 的 TCP 端口通不通。
// 用来区分 FetchMessage 超时的两种情况: 真没消息(broker 通) vs 连不上(端口也不通)。
func (c *Consumer) brokerAlive() bool {
	for _, b := range c.brokers {
		conn, err := net.DialTimeout("tcp", b, 800*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
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

	failStreak := 0
	var lastRebuild time.Time
	// rebuild 卡死后按原样重建 reader 自愈。加了 10 秒最小间隔:
	// 重建太频繁会把刚 join 到一半的新 reader 又干掉, 永远恢复不了(实测踩过)。
	rebuild := func(why string) {
		if !lastRebuild.IsZero() && time.Since(lastRebuild) < 10*time.Second {
			return // 刚重建过, 再观察一会
		}
		lastRebuild = time.Now()
		flush() // 攒着的先落库, 别跟着 reader 一起陪葬
		logx.Errorf("kafka reader 持续读取失败(%s), 重建 reader 自愈", why)
		if err := c.reader.Close(); err != nil {
			logx.Errorf("关闭旧 reader 失败: %v", err)
		}
		c.reader = c.newReader()
		failStreak = 0
	}

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
				// 超时有两种可能: 真没消息, 或者 broker 连不上(reader 内部把错吞成了超时)。
				// 用 TCP 探测区分: 连不上才计入失败, 别把正常空闲也当成故障。
				if !c.brokerAlive() {
					failStreak++
					if failStreak >= 3 {
						rebuild("broker 不可达")
					}
				} else {
					failStreak = 0
				}
				continue
			}
			// 明确的错误(连接被拒等), 同样累计; kafka-go 首连失败后可能一直吐同一个错
			failStreak++
			if failStreak >= 10 {
				rebuild(err.Error())
			}
			// Kafka 连不上时别空转把 CPU 跑满, 歇一秒再试
			time.Sleep(time.Second)
			continue
		}
		failStreak = 0

		r, ok, reason := c.parse(ctx, msg.Value)
		if !ok {
			if reason != "" {
				logx.Errorf("脏数据, 丢弃: %s", reason)
			}
			_ = c.reader.CommitMessages(ctx, msg) // 跳过和丢弃的消息都不重试
			continue
		}
		batch = append(batch, r)
		msgs = append(msgs, msg)
		if len(batch) >= batchSize {
			flush()
		}
	}
}
