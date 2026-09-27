package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"onepark/common/kafka"
)

// publishedMsg 记录一次发布调用的 topic/key/value.
type publishedMsg struct {
	topic string
	key   string
	value string
}

// fakePublisher 复刻生产者行为: 记录调用, 并可按"前 N 次失败"注入故障.
type fakePublisher struct {
	calls     []publishedMsg
	failFirst int
}

func (f *fakePublisher) Publish(_ context.Context, topic string, key, value []byte) error {
	f.calls = append(f.calls, publishedMsg{topic: topic, key: string(key), value: string(value)})
	if len(f.calls) <= f.failFirst {
		return errors.New("kafka unavailable")
	}
	return nil
}

// newTestNotifier 构造不等待的重试配置(退避改 1ms, 避免单测被真实退避拖慢).
func newTestNotifier(pub Publisher, topic string) *KafkaNotifier {
	n := NewKafkaNotifier(pub, topic)
	n.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return n
}

// sampleEvent 返回一条字段齐全的告警事件.
func sampleEvent() AlarmEvent {
	return AlarmEvent{
		AlarmID:   "AL2026091699c67157",
		AlarmType: "intrusion",
		DeviceID:  "door-01",
		TenantID:  7,
		AreaID:    12,
		Severity:  3,
		Status:    2,
		Content:   "门禁设备 door-01 检测到非法闯入",
	}
}

// TestDefaultTopic_MatchesCommonConstant 通知器默认 topic 必须与 common/kafka 的常量一致,
// 否则 M5 按公共常量写消费者时会永远收不到消息.
func TestDefaultTopic_MatchesCommonConstant(t *testing.T) {
	if DefaultTopic != kafka.TopicAlarmEvent {
		t.Fatalf("默认 topic 与 common/kafka.TopicAlarmEvent 不一致: %s vs %s", DefaultTopic, kafka.TopicAlarmEvent)
	}
}

// TestAlarmResolved_PublishesEvent 发布: 默认 topic + 分区键=device_id + 载荷字段完整.
func TestAlarmResolved_PublishesEvent(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")

	if n.Topic() != DefaultTopic {
		t.Errorf("未指定 topic 时应使用默认值 %s, 实际 %s", DefaultTopic, n.Topic())
	}
	if err := n.AlarmResolved(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("应只发布一次, 实际 %d 次", len(pub.calls))
	}

	call := pub.calls[0]
	if call.topic != DefaultTopic {
		t.Errorf("topic 应为 %s, 实际 %s", DefaultTopic, call.topic)
	}
	// 分区键用 device_id: 与 M1/消费侧"同设备事件保序"的约定一致.
	if call.key != "door-01" {
		t.Errorf("分区键应为 device_id, 实际 %s", call.key)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(call.value), &got); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v, value=%s", err, call.value)
	}
	if got["action"] != ActionResolved {
		t.Errorf("未显式指定 action 时应补 resolve, 实际 %v", got["action"])
	}
	if ts, ok := got["timestamp"].(float64); !ok || ts <= 0 {
		t.Errorf("未指定 timestamp 时应补当前毫秒时间戳, 实际 %v", got["timestamp"])
	}
}

// TestAlarmCreated_PublishesEvent 告警产生事件: action 缺省补 create, 其余字段与告警一致.
func TestAlarmCreated_PublishesEvent(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")

	ev := sampleEvent()
	ev.Action = ""        // 交给实现补齐
	ev.Status = 0         // 新告警是未处理态
	ev.Timestamp = 1789000000000

	if err := n.AlarmCreated(context.Background(), ev); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("应只发布一次, 实际 %d 次", len(pub.calls))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(pub.calls[0].value), &got); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v", err)
	}
	if got["action"] != ActionCreated {
		t.Errorf("未显式指定 action 时应补 create, 实际 %v", got["action"])
	}
	if got["status"] != float64(0) {
		t.Errorf("产生事件的状态应为未处理(0), 实际 %v", got["status"])
	}
	if got["timestamp"] != float64(1789000000000) {
		t.Errorf("timestamp 应原样透传, 实际 %v", got["timestamp"])
	}
}

// TestCreatedAndResolvedSharePartitionKey 同一设备的 create 与 resolve 必须落在同一分区.
//
// 这条是"消费方能按序看到告警一生"的前提: 分区键都是 device_id, 若哪天 create 改用了别的键
// (比如 alarm_id), 同一告警的两条事件会散到不同分区, M5 就可能先收到 resolve 再收到 create,
// 表现为"单据刚建就被关闭"或"关单时查无此单"。
func TestCreatedAndResolvedSharePartitionKey(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")

	created := sampleEvent()
	created.Status = 0
	resolved := sampleEvent()
	resolved.Status = 2

	if err := n.AlarmCreated(context.Background(), created); err != nil {
		t.Fatalf("发布产生事件失败: %v", err)
	}
	if err := n.AlarmResolved(context.Background(), resolved); err != nil {
		t.Fatalf("发布解决事件失败: %v", err)
	}
	if len(pub.calls) != 2 {
		t.Fatalf("应发布 2 次, 实际 %d 次", len(pub.calls))
	}
	if pub.calls[0].key != pub.calls[1].key {
		t.Errorf("两类事件的分区键必须一致: create=%s resolve=%s", pub.calls[0].key, pub.calls[1].key)
	}
	if pub.calls[0].key != "door-01" {
		t.Errorf("分区键应为 device_id, 实际 %s", pub.calls[0].key)
	}
}

// TestAlarmResolved_ContractFields 锁定对外字段名: M5 依赖这些键名解析, 改名即破坏契约.
func TestAlarmResolved_ContractFields(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")
	ev := sampleEvent()
	ev.Timestamp = 1789000000000

	if err := n.AlarmResolved(context.Background(), ev); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(pub.calls[0].value), &got); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v", err)
	}

	want := map[string]any{
		"alarm_id":   "AL2026091699c67157",
		"action":     "resolve",
		"alarm_type": "intrusion",
		"device_id":  "door-01",
		"tenant_id":  float64(7),
		"area_id":    float64(12),
		"severity":   float64(3),
		// severity=3(严重) 按默认映射换算为 M5 优先级 2(高).
		"priority":  float64(2),
		"status":    float64(2),
		"content":   "门禁设备 door-01 检测到非法闯入",
		"timestamp": float64(1789000000000),
	}
	if len(got) != len(want) {
		t.Errorf("载荷字段数量与契约不符: 期望 %d 个, 实际 %d 个: %v", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("载荷字段 %s 应为 %v, 实际 %v", k, v, got[k])
		}
	}
}

// TestAlarmResolved_CustomTopic 配置了 topic 时按配置发送(便于 M5/组长改名后零改代码).
func TestAlarmResolved_CustomTopic(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "onepark.alarm.event.dev")

	if err := n.AlarmResolved(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if pub.calls[0].topic != "onepark.alarm.event.dev" {
		t.Errorf("应使用配置的 topic, 实际 %s", pub.calls[0].topic)
	}
}

// TestAlarmResolved_RetryThenSucceed 前两次失败后重试成功.
func TestAlarmResolved_RetryThenSucceed(t *testing.T) {
	pub := &fakePublisher{failFirst: 2}
	n := newTestNotifier(pub, "")

	if err := n.AlarmResolved(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if len(pub.calls) != 3 {
		t.Errorf("应尝试 3 次(1 次 + 2 次重试), 实际 %d 次", len(pub.calls))
	}
}

// TestAlarmResolved_AllAttemptsFail 全部尝试失败必须上抛错误, 由调用方提示补偿(不能静默成功).
func TestAlarmResolved_AllAttemptsFail(t *testing.T) {
	pub := &fakePublisher{failFirst: 99}
	n := newTestNotifier(pub, "")

	err := n.AlarmResolved(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("全部尝试失败必须返回错误")
	}
	if len(pub.calls) != 3 {
		t.Errorf("应尝试 3 次, 实际 %d 次", len(pub.calls))
	}
	if !strings.Contains(err.Error(), DefaultTopic) || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("错误信息应含 topic 与尝试次数便于定位: %v", err)
	}
	if !strings.Contains(err.Error(), "kafka unavailable") {
		t.Errorf("错误信息应保留底层原因: %v", err)
	}
}

// blockingPublisher 模拟"broker 不可达、底层客户端一直内部重试"的生产者: 阻塞直到 ctx 结束.
type blockingPublisher struct{ calls int }

func (b *blockingPublisher) Publish(ctx context.Context, _ string, _, _ []byte) error {
	b.calls++
	<-ctx.Done()
	return ctx.Err()
}

// TestAlarmResolved_PublishTimeoutBounded 单次发布超时必须生效:
// kafka-go 的 Writer 自身会重试, 没有这层超时的话 broker 故障会把 resolve 接口拖挂住.
func TestAlarmResolved_PublishTimeoutBounded(t *testing.T) {
	pub := &blockingPublisher{}
	n := NewKafkaNotifier(pub, "")
	n.backoff = []time.Duration{0, 0}        // 退避置零, 只测超时本身
	n.publishTimeout = 30 * time.Millisecond // 缩短以加快用例

	start := time.Now()
	err := n.AlarmResolved(context.Background(), sampleEvent())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("发布超时必须返回错误")
	}
	if elapsed > time.Second {
		t.Errorf("单次发布超时未生效, 耗时 %v", elapsed)
	}
	if pub.calls != 3 {
		t.Errorf("超时后仍应完成 3 次尝试, 实际 %d 次", pub.calls)
	}
}

// TestAlarmResolved_NilPublisher 未初始化生产者时报错而不是 panic.
func TestAlarmResolved_NilPublisher(t *testing.T) {
	n := NewKafkaNotifier(nil, "")
	if err := n.AlarmResolved(context.Background(), sampleEvent()); err == nil {
		t.Fatal("生产者未初始化必须返回错误")
	}
}

// TestAlarmResolved_ContextCanceled ctx 取消时应立即退出重试, 不把接口请求拖到超时.
func TestAlarmResolved_ContextCanceled(t *testing.T) {
	pub := &fakePublisher{failFirst: 99}
	n := NewKafkaNotifier(pub, "")
	n.backoff = []time.Duration{5 * time.Second, 5 * time.Second} // 若未响应 ctx 取消, 用例会明显变慢

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := n.AlarmResolved(ctx, sampleEvent())
	if err == nil {
		t.Fatal("ctx 已取消时必须返回错误")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("错误信息应表明被取消: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Errorf("取消后不应继续重试, 实际尝试 %d 次", len(pub.calls))
	}
}
