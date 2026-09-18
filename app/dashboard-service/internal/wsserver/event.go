// 大屏事件增量推送(组长一周计划书 周四 P0「Kafka 消费增量推送」)。
//
// 为什么事件和快照必须一起用, 而不是二选一:
//   - 只有 5s 快照: 事件发生后最坏要等 5s 才可见, 且聚合结果有 30s 缓存 ——
//     事件的影响可能被旧缓存盖住最长 30s。这恰恰是"事件驱动"要解决的问题。
//   - 只有事件: 前端拿不到全量卡片, 断线重连后没有数据基线。
//
// **本文件的关键是缓存穿透**: 事件到达时只置一个"脏"标记(纯内存写, 极轻量),
// 由快照循环在推送前统一失效缓存再重新聚合。这样做的两个理由:
//  1. 天然节流 —— 告警风暴时每 5s 最多失效一次, 不会每个事件都去 SCAN Redis;
//  2. 不丢效果 —— 脏标记在被取走前一直保留, 最后一条事件的影响必然体现在下一次聚合里。
//     (若在事件回调里直接"节流失效", 节流窗口内到达的事件就可能被随后的聚合写成旧值。)
package wsserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dashboard-service/internal/config"
	"onepark/app/dashboard-service/internal/wshub"
	"onepark/common/kafka"
)

// Dirty 跨 goroutine 的「聚合缓存已过期」标记.
type Dirty struct {
	mu   sync.Mutex
	flag bool
}

// NewDirty 构造脏标记.
func NewDirty() *Dirty { return &Dirty{} }

// Mark 置位(事件到达时调用). 只写一个 bool, 不做任何 I/O, 可在消费回调里高频调用.
func (d *Dirty) Mark() {
	d.mu.Lock()
	d.flag = true
	d.mu.Unlock()
}

// Take 取走标记并清零; 返回 true 表示"自上次取走后有过事件".
func (d *Dirty) Take() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.flag
	d.flag = false
	return v
}

// eventMsg 推送给大屏的增量消息.
//
// Data 直接透传 Kafka 原始消息体: 本服务**不解析**各模块的事件结构 ——
// schema 归生产方(M2/M3)所有, 在这里再定义一份就等于把同一份契约写两遍,
// 上游改字段时这边会静默失配。前端按 source 字段自行分发即可。
type eventMsg struct {
	Type       string          `json:"type"`        // 固定 "event"
	Source     string          `json:"source"`      // alarm / workorder
	Topic      string          `json:"topic"`       // 原始 topic, 便于前端/排查定位
	ReceivedAt int64           `json:"received_at"` // 服务端接收时间(秒)
	Data       json.RawMessage `json:"data"`
}

// EventRunner 消费一个事件 topic, 把消息作为增量广播给大屏.
type EventRunner struct {
	logx.Logger
	consumer *kafka.Consumer
	topic    string
	group    string
	hub      *wshub.Hub
	dirty    *Dirty
}

// StartEventConsumers 按配置构造各 topic 的消费者.
//
// 返回空切片表示未启用(或配置不完整), 调用方据此跳过启动 ——
// 「默认关闭」是刻意设计: 共享 broker 上误开消费者会真实读取别人的消息并提交位移。
func StartEventConsumers(ctx context.Context, c config.Config, hub *wshub.Hub, dirty *Dirty) []*EventRunner {
	logger := logx.WithContext(ctx)

	if !c.Kafka.Enabled {
		logger.Info("[ws] 事件推送未启用(Kafka.Enabled=false), 仅推送周期快照")
		return nil
	}
	if strings.TrimSpace(c.Kafka.Brokers) == "" {
		logger.Error("[ws] Kafka.Enabled=true 但 Brokers 为空, 事件推送不启动")
		return nil
	}
	if len(c.Kafka.Topics) == 0 {
		// 不在这里猜默认 topic: 共享 broker 上"猜错就消费别人的消息", 必须由配置显式指定
		logger.Error("[ws] Kafka.Enabled=true 但 Topics 为空, 事件推送不启动")
		return nil
	}

	runners := make([]*EventRunner, 0, len(c.Kafka.Topics))
	for _, topic := range c.Kafka.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		// 消费组必须逐 topic 区分: Kafka 位移按 (group, topic) 提交,
		// 同一个 group 消费多个 topic 虽然合法, 但排查位移时无法区分是哪个 topic 卡住。
		group := fmt.Sprintf("%s-%s", c.Kafka.Group, topic)
		runners = append(runners, &EventRunner{
			Logger:   logx.WithContext(ctx),
			consumer: kafka.NewConsumer(c.Kafka.Brokers, topic, group),
			topic:    topic,
			group:    group,
			hub:      hub,
			dirty:    dirty,
		})
	}
	return runners
}

// Start 阻塞运行消费循环, 应在独立 goroutine 中调用; ctx 取消即退出.
func (r *EventRunner) Start(ctx context.Context) {
	r.Infof("[ws] 开始消费事件: topic=%s, group=%s", r.topic, r.group)

	// 用 common/kafka.Message(segmentio 的别名)而不是直接依赖 segmentio 包
	err := r.consumer.Consume(ctx, func(_ context.Context, msg kafka.Message) error {
		r.handle(msg)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		r.Errorf("[ws] 事件消费循环异常退出: topic=%s, err=%v", r.topic, err)
	}

	r.Infof("[ws] 事件消费循环已停止: topic=%s", r.topic)
}

// Close 释放消费者资源.
func (r *EventRunner) Close() error {
	return r.consumer.Close()
}

// handle 广播一条增量消息, 并标记聚合缓存已过期.
//
// 刻意不返回 error: 坏消息(空体/非法 JSON)记日志后跳过即可。
// 若返回 error, common/kafka 不会提交位移, 一条毒消息会把整个分区永久卡死。
func (r *EventRunner) handle(msg kafka.Message) {
	// 无论有没有在线大屏都要标记: HTTP 的 /overview 接口同样吃这份缓存
	r.dirty.Mark()

	payload, err := json.Marshal(eventMsg{
		Type:       "event",
		Source:     eventSource(r.topic),
		Topic:      r.topic,
		ReceivedAt: time.Now().Unix(),
		Data:       json.RawMessage(msg.Value),
	})
	if err != nil {
		r.Errorf("[ws] 事件序列化失败: topic=%s, err=%v", r.topic, err)
		return
	}

	if n := r.hub.Count(); n > 0 {
		r.hub.Broadcast(payload)
		r.Debugf("[ws] 已推送事件: topic=%s, clients=%d", r.topic, n)
	}
}

// eventSource 把 topic 名映射为对前端稳定的来源标识.
func eventSource(topic string) string {
	switch topic {
	case kafka.TopicAlarm:
		return "alarm"
	case kafka.TopicWorkorder:
		return "workorder"
	default:
		return topic
	}
}
