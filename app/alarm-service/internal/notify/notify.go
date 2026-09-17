// Package notify 负责 M3 → M5 的告警事件通知(docs/m3/04 §1.1 #40):
// 告警解决后生产 Kafka 事件, M5 侧据此更新大屏/触发调度闭环.
//
// 契约来源: docs/m3/04 §3.2(复用 common.AlarmEvent 字段) + M5 确认书 §5 的 topic 提案
// (`onepark.alarm.event`, M3 生产 / M5 消费, 幂等键 alarm_id).
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DefaultTopic 默认生产 topic, 与 common/kafka.TopicAlarmEvent 同值.
const DefaultTopic = "onepark.alarm.event"

// ActionResolved 告警"已解决"动作.
// 载荷里带 action 是为了让 M5 能区分"告警产生"与"告警解决"这两类事件 ——
// 两者幂等键同为 alarm_id, 消费方若不加区分就无法判断该建单还是该关单.
const ActionResolved = "resolve"

// AlarmEvent 告警事件载荷(JSON, snake_case 与 proto/common 字段命名保持一致).
//
// ⚠️ severity 口径待与 M5 书面确认: 本服务沿用 docs/m3/04 §5.1 的
// 1提示 / 2一般 / 3严重 / 4紧急; 而 M5 确认书 §3.1 的 AlarmStatResponse 注释写的是
// "severity = 1 critical / 2 major / 3 minor"(1 最严重), 两者方向相反.
// 在确认前一律按 M3 自身语义发送, 不擅自翻转 —— 翻转会让告警等级在本服务与 M5 之间含义不一致.
type AlarmEvent struct {
	AlarmID   string `json:"alarm_id"`   // 业务编号 alarm_no, 与 M5 dispatch_task.uk_alarm_id 对齐
	Action    string `json:"action"`     // resolve(当前仅终态发事件)
	AlarmType string `json:"alarm_type"` // M3 event_type(intrusion/door_timeout/...), M5 用于技能匹配
	DeviceID  string `json:"device_id"`
	TenantID  int64  `json:"tenant_id"`
	AreaID    int64  `json:"area_id"`  // M3 只有区域ID; M5 提案要的 zone_code 需等区域编码映射就绪(不臆造)
	Severity  int8   `json:"severity"` // 1提示~4紧急(见上方口径提示)
	Status    int8   `json:"status"`   // 流转后的状态, 当前恒为 2(已解决)
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"` // 毫秒: 事件发生(解决)时间
}

// Publisher 消息生产抽象, 由 common/kafka.Producer 实现.
// 抽成接口是为了让"重试/最终失败上抛"这两条分支能被单测覆盖, 不依赖真实 broker.
type Publisher interface {
	Publish(ctx context.Context, topic string, key, value []byte) error
}

// Notifier 告警事件通知抽象, 由 ServiceContext 注入 logic.
type Notifier interface {
	// AlarmResolved 通知 M5 "该告警已解决"; 返回错误表示事件尚未送达, 需要补偿.
	AlarmResolved(ctx context.Context, ev AlarmEvent) error
}

// retryBackoff 发布重试退避: 共 3 次尝试, 累计 600ms.
// 取这么短是因为它串在 HTTP 请求链路上: 长时间阻塞会把"通知失败"放大成"接口超时",
// 真正的兜底应交给调用方感知错误码后的补偿动作(见 M3-E-1008)。
var retryBackoff = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond}

// publishTimeout 单次发布的超时上限.
// 必需的原因: kafka-go 的 Writer 在 broker 不可达时会按自身 MaxAttempts(默认 10)内部重试,
// 不设上限的话"broker 挂了"会让 resolve 接口长时间挂起; 显式超时后由本包的重试与
// 上层的补偿错误码接管, 最坏耗时 ≈ 3×1.5s + 0.6s 退避.
const publishTimeout = 1500 * time.Millisecond

var _ Notifier = (*KafkaNotifier)(nil)

// KafkaNotifier 基于 Kafka 的告警事件通知实现.
type KafkaNotifier struct {
	publisher      Publisher
	topic          string
	backoff        []time.Duration
	publishTimeout time.Duration
}

// NewKafkaNotifier 创建通知器; topic 为空时用 DefaultTopic.
func NewKafkaNotifier(publisher Publisher, topic string) *KafkaNotifier {
	if topic == "" {
		topic = DefaultTopic
	}
	return &KafkaNotifier{
		publisher:      publisher,
		topic:          topic,
		backoff:        retryBackoff,
		publishTimeout: publishTimeout,
	}
}

// Topic 返回实际使用的 topic, 供启动日志定位"事件发到哪去了".
func (n *KafkaNotifier) Topic() string { return n.topic }

// AlarmResolved 发布"告警已解决"事件.
// 分区键用 device_id: 与 M1/消费侧"同设备事件保序"的约定一致(docs/m3/06 §1).
func (n *KafkaNotifier) AlarmResolved(ctx context.Context, ev AlarmEvent) error {
	if n.publisher == nil {
		return fmt.Errorf("notify publisher not initialized")
	}
	if ev.Action == "" {
		ev.Action = ActionResolved
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	value, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("notify marshal alarm event: %w", err)
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		lastErr = n.publishOnce(ctx, ev.DeviceID, value)
		if lastErr == nil {
			return nil
		}
		if attempt >= len(n.backoff) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("notify alarm event aborted: %w", ctx.Err())
		case <-time.After(n.backoff[attempt]):
		}
	}

	return fmt.Errorf("notify alarm event to topic %s failed after %d attempts: %w",
		n.topic, len(n.backoff)+1, lastErr)
}

// publishOnce 发一次, 并用 publishTimeout 兜住底层客户端的内部重试, 避免请求被长时间挂住.
func (n *KafkaNotifier) publishOnce(ctx context.Context, key string, value []byte) error {
	if n.publishTimeout <= 0 {
		return n.publisher.Publish(ctx, n.topic, []byte(key), value)
	}
	pubCtx, cancel := context.WithTimeout(ctx, n.publishTimeout)
	defer cancel()
	return n.publisher.Publish(pubCtx, n.topic, []byte(key), value)
}
