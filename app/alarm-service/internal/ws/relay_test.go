package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakeRelay 在内存中模拟总线: publish 记录在案, Receive 从 inbound 通道取消息.
// recvErrs 可注入若干次"订阅中断"错误, 用于验证订阅循环的重试行为.
type fakeRelay struct {
	mu         sync.Mutex
	published  []*BusMessage
	publishErr error
	recvErrs   []error
	closed     bool

	inbound chan *BusMessage
}

func newFakeRelay() *fakeRelay {
	return &fakeRelay{inbound: make(chan *BusMessage, 16)}
}

func (f *fakeRelay) Publish(_ context.Context, msg *BusMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, msg)
	return nil
}

func (f *fakeRelay) Receive(ctx context.Context) (*BusMessage, error) {
	f.mu.Lock()
	if len(f.recvErrs) > 0 {
		err := f.recvErrs[0]
		f.recvErrs = f.recvErrs[1:]
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-f.inbound:
		return msg, nil
	}
}

func (f *fakeRelay) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// waitPublished 等待至少 n 条消息被 publish.
// Push 的发布是异步的(不阻塞调用方), 断言前必须等待.
func (f *fakeRelay) waitPublished(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.published)
		f.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// publishedMessages 返回已发布消息的快照.
func (f *fakeRelay) publishedMessages() []*BusMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*BusMessage(nil), f.published...)
}

// startRelay 启一个带中继的 Hub, 测试结束自动取消订阅循环.
func startRelay(t *testing.T, relay Relay) *Hub {
	t.Helper()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub.StartRelay(ctx, relay)
	return hub
}

// ---------------------------------------------------------------------------
// 跨实例投递路径
// ---------------------------------------------------------------------------

// TestHub_RelayModePublishesToBusWithoutLocalDirectDelivery 跨实例模式下:
// 消息只交给总线, 本地不直投 —— 本地投递由订阅回声完成, 因此不会重复推送.
func TestHub_RelayModePublishesToBusWithoutLocalDirectDelivery(t *testing.T) {
	relay := newFakeRelay()
	hub := startRelay(t, relay)
	c := addClient(t, hub, 1)

	if !hub.Push(1, NewEnvelope(TypeAlarmCreated, AlarmEvent{AlarmID: "AL-20260922-0001"}, "rid-1")) {
		t.Fatal("跨实例模式 Push 应返回已受理")
	}
	if !relay.waitPublished(1, time.Second) {
		t.Fatal("消息未发布到总线")
	}

	// 回声尚未回来时, 本地不应有消息(否则 redis 回声会造成重复推送).
	if msg, ok := recv(c, 100*time.Millisecond); ok {
		t.Fatalf("跨实例模式不应本地直投: %s", msg)
	}

	published := relay.publishedMessages()[0]
	if published.TenantID != 1 {
		t.Errorf("总线消息必须带租户维度: %+v", published)
	}
	var env Envelope
	if err := json.Unmarshal(published.Payload, &env); err != nil {
		t.Fatalf("总线载荷应为客户端信封: %v", err)
	}
	if env.Type != TypeAlarmCreated || env.RequestId != "rid-1" {
		t.Errorf("信封内容异常: %+v", env)
	}

	// 模拟 Redis 把消息回声给本实例: 此时才应投递给本地客户端, 且只投一次.
	relay.inbound <- published
	if _, ok := recv(c, time.Second); !ok {
		t.Fatal("订阅回声应投递给本实例客户端")
	}
	if msg, ok := recv(c, 100*time.Millisecond); ok {
		t.Fatalf("同一条广播被重复投递: %s", msg)
	}
}

// TestHub_RelayPublishFailureFallsBackLocally 总线不可用时降级为本地投递:
// 至少保证本实例大屏能看到告警, 不让告警静默消失.
func TestHub_RelayPublishFailureFallsBackLocally(t *testing.T) {
	relay := newFakeRelay()
	relay.publishErr = errors.New("redis down")
	hub := startRelay(t, relay)
	c := addClient(t, hub, 1)

	hub.Push(1, NewEnvelope(TypeAlarmAck, AlarmEvent{AlarmID: "AL-20260922-0005", Status: 1}, ""))

	// 降级发生在 goroutine 内, 轮询等待.
	msg, ok := recv(c, 2*time.Second)
	if !ok {
		t.Fatal("总线故障时应降级本地投递")
	}
	var env Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("消息非法: %v", err)
	}
	if env.Type != TypeAlarmAck {
		t.Errorf("信封类型异常: %s", env.Type)
	}
	if len(relay.publishedMessages()) != 0 {
		t.Error("发布失败不应留下成功记录")
	}
}

// TestHub_RelayInboundTenantIsolated 从总线收到的消息同样必须按园区过滤.
func TestHub_RelayInboundTenantIsolated(t *testing.T) {
	relay := newFakeRelay()
	hub := startRelay(t, relay)
	a := addClient(t, hub, 1)
	b := addClient(t, hub, 2)

	payload, err := NewEnvelope(TypeAlarmCreated, AlarmEvent{AlarmID: "AL-20260922-0007", DeviceID: "door-01"}, "r").Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	relay.inbound <- NewBusMessage(1, payload)

	if _, ok := recv(a, time.Second); !ok {
		t.Fatal("园区1的连接应收到总线消息")
	}
	if msg, ok := recv(b, 100*time.Millisecond); ok {
		t.Fatalf("园区2的连接不应收到园区1的告警: %s", msg)
	}
}

// TestHub_RelayReceiveErrorKeepsLoopAlive 订阅中断必须退避重试而非退出循环 ——
// 循环一旦退出, 该实例的客户端会静默地再也收不到推送.
func TestHub_RelayReceiveErrorKeepsLoopAlive(t *testing.T) {
	relay := newFakeRelay()
	relay.recvErrs = []error{errors.New("connection reset by peer")}
	hub := startRelay(t, relay)
	c := addClient(t, hub, 3)

	payload, err := NewEnvelope(TypeAlarmResolved, AlarmEvent{AlarmID: "AL-20260922-0009", Status: 2}, "").Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	relay.inbound <- NewBusMessage(3, payload)

	// 首次 Receive 报错 → 退避 relayRetryDelay 后重试 → 仍应送达.
	if _, ok := recv(c, 3*time.Second); !ok {
		t.Fatal("Receive 报错后订阅循环应重试, 而非退出")
	}
}

// TestHub_NoRelayKeepsSingleInstanceBehaviour 未启用中继时保持原有单实例行为.
func TestHub_NoRelayKeepsSingleInstanceBehaviour(t *testing.T) {
	hub := NewHub()
	c := addClient(t, hub, 1)

	if !hub.Push(1, NewEnvelope(TypeAlarmCreated, AlarmEvent{AlarmID: "AL-20260922-0001"}, "")) {
		t.Error("单实例模式有在线连接时应返回 true")
	}
	if _, ok := recv(c, time.Second); !ok {
		t.Error("单实例模式应本地直投")
	}
	// 无在线连接时返回 false 且不 panic.
	if NewHub().Push(1, NewEnvelope(TypeAlarmCreated, AlarmEvent{}, "")) {
		t.Error("无在线连接应返回 false")
	}
}

// TestHub_StartRelayNilSafe 未配置中继(或 Hub 为 nil)时不得 panic.
func TestHub_StartRelayNilSafe(t *testing.T) {
	hub := NewHub()
	hub.StartRelay(context.Background(), nil)
	if _, ok := recv(addClient(t, hub, 1), 50*time.Millisecond); ok {
		t.Error("未注入中继不应有消息")
	}

	var nilHub *Hub
	nilHub.StartRelay(context.Background(), newFakeRelay())
	nilHub.Push(1, NewEnvelope(TypeAlarmAck, AlarmEvent{}, ""))
}

// TestBusMessage_EncodeRoundTrip 总线消息编解码保真(含租户维度).
func TestBusMessage_EncodeRoundTrip(t *testing.T) {
	payload := []byte(`{"type":"alarm.created","ts":1}`)
	raw, err := NewBusMessage(42, payload).Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got BusMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TenantID != 42 {
		t.Errorf("租户维度丢失: %d", got.TenantID)
	}
	if string(got.Payload) != string(payload) {
		t.Errorf("载荷不保真: %s", got.Payload)
	}
}

// ---------------------------------------------------------------------------
// Redis 实现: 只覆盖能脱离真实 Redis 验证的部分(Publish 语义)
// ---------------------------------------------------------------------------

// fakeRedisPublishClient 只实现 Publish; Subscribe 在本组用例中不会被调用.
type fakeRedisPublishClient struct {
	channel string
	payload interface{}
	err     error
}

func (f *fakeRedisPublishClient) Publish(_ context.Context, channel string, message interface{}) *goredis.IntCmd {
	f.channel, f.payload = channel, message
	return goredis.NewIntResult(1, f.err)
}

func (f *fakeRedisPublishClient) Subscribe(context.Context, ...string) *goredis.PubSub {
	panic("本用例不应建立订阅")
}

// TestRedisRelay_Publish 校验发到配置的频道, 且载荷是可反序列化的总线消息.
func TestRedisRelay_Publish(t *testing.T) {
	client := &fakeRedisPublishClient{}
	relay := NewRedisRelay(client, BroadcastChannel)

	if err := relay.Publish(context.Background(), NewBusMessage(7, []byte(`{"t":1}`))); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if client.channel != BroadcastChannel {
		t.Errorf("频道错误: %s", client.channel)
	}
	raw, ok := client.payload.([]byte)
	if !ok {
		t.Fatalf("载荷应为字节切片, 实际 %T", client.payload)
	}
	var m BusMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("载荷应为总线消息: %v", err)
	}
	if m.TenantID != 7 {
		t.Errorf("租户维度错误: %d", m.TenantID)
	}
}

// TestRedisRelay_PublishErrorPropagates 发布失败必须上抛, 由 Hub 降级为本地投递.
func TestRedisRelay_PublishErrorPropagates(t *testing.T) {
	client := &fakeRedisPublishClient{err: errors.New("redis down")}
	relay := NewRedisRelay(client, BroadcastChannel)

	if err := relay.Publish(context.Background(), NewBusMessage(1, nil)); err == nil {
		t.Fatal("发布失败应返回错误")
	}
	if err := relay.Close(); err != nil {
		t.Errorf("未建立订阅时 Close 应安全: %v", err)
	}
}
