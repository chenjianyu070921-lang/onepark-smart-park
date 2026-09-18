package wsserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dashboard-service/internal/config"
	"onepark/app/dashboard-service/internal/wshub"
	"onepark/common/kafka"
)

// ---------- Dirty: 事件驱动的节流核心 ----------

func TestDirty_Semantics(t *testing.T) {
	d := NewDirty()

	if d.Take() {
		t.Error("初始状态不应为脏")
	}

	d.Mark()
	if !d.Take() {
		t.Error("Mark 之后 Take 应为 true")
	}
	if d.Take() {
		t.Error("Take 必须清零, 第二次应为 false(否则每轮快照都会白失效一次缓存)")
	}

	// 连续 Mark 只等同于一次: 这是节流的根据
	d.Mark()
	d.Mark()
	d.Mark()
	if !d.Take() {
		t.Error("多次 Mark 后 Take 应为 true")
	}
	if d.Take() {
		t.Error("多次 Mark 不应累积成多次 Take")
	}
}

// TestDirty_Concurrent 并发 Mark 时标记不能丢 —— 丢了就等于这次事件的缓存失效被吞掉,
// 大屏会继续显示最多 30s 的旧数据。
func TestDirty_Concurrent(t *testing.T) {
	d := NewDirty()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Mark()
		}()
	}
	wg.Wait()

	if !d.Take() {
		t.Fatal("100 个并发 Mark 之后 Take 应为 true")
	}
}

// ---------- topic -> source 映射 ----------

func TestEventSource(t *testing.T) {
	cases := map[string]string{
		kafka.TopicAlarm:     "alarm",
		kafka.TopicWorkorder: "workorder",
		"some-other-topic":   "some-other-topic", // 未知 topic 原样返回, 不丢信息
	}
	for topic, want := range cases {
		if got := eventSource(topic); got != want {
			t.Errorf("eventSource(%q) = %q, 期望 %q", topic, got, want)
		}
	}
}

// ---------- 消费者装配: 默认关闭且不猜 topic ----------

func TestStartEventConsumers(t *testing.T) {
	ctx := context.Background()
	hub := wshub.NewHub()
	dirty := NewDirty()

	t.Run("未启用时返回空", func(t *testing.T) {
		got := StartEventConsumers(ctx, config.Config{}, hub, dirty)
		if len(got) != 0 {
			t.Errorf("Enabled=false 应返回空, 实际 %d 个", len(got))
		}
	})

	t.Run("启用但Brokers为空时返回空", func(t *testing.T) {
		c := config.Config{}
		c.Kafka.Enabled = true
		c.Kafka.Topics = []string{kafka.TopicAlarm}
		if got := StartEventConsumers(ctx, c, hub, dirty); len(got) != 0 {
			t.Errorf("Brokers 为空应返回空, 实际 %d 个", len(got))
		}
	})

	t.Run("启用但Topics为空时返回空(不猜默认值)", func(t *testing.T) {
		// 这条是刻意设计: 共享 broker 上"猜错 topic"就是去读别人的消息
		c := config.Config{}
		c.Kafka.Enabled = true
		c.Kafka.Brokers = "127.0.0.1:9092"
		if got := StartEventConsumers(ctx, c, hub, dirty); len(got) != 0 {
			t.Errorf("Topics 为空应返回空, 实际 %d 个", len(got))
		}
	})

	t.Run("启用后逐个topic建消费者且消费组带topic名", func(t *testing.T) {
		c := config.Config{}
		c.Kafka.Enabled = true
		c.Kafka.Brokers = "127.0.0.1:9092"
		c.Kafka.Topics = []string{kafka.TopicAlarm, kafka.TopicWorkorder}
		c.Kafka.Group = "m5-dashboard-dev"

		runners := StartEventConsumers(ctx, c, hub, dirty)
		if len(runners) != 2 {
			t.Fatalf("消费者数 = %d, 期望 2", len(runners))
		}
		t.Cleanup(func() {
			for _, r := range runners {
				_ = r.Close()
			}
		})

		for _, r := range runners {
			// 消费组必须带 topic 名: 否则两个 topic 共用一个位移, 排查时无法区分谁卡住
			if !strings.HasPrefix(r.group, "m5-dashboard-dev-") {
				t.Errorf("消费组 %q 未带前缀", r.group)
			}
			if !strings.Contains(r.group, r.topic) {
				t.Errorf("消费组 %q 未包含 topic %q", r.group, r.topic)
			}
		}
	})
}

// ---------- 广播行为: 用真实 WS 连接验证 ----------

// waitFor 轮询等待条件成立.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件在 2s 内未满足")
}

// startTestWS 起一个真实升级 WS 的测试服务器并注册到 hub.
func startTestWS(t *testing.T, hub *wshub.Hub) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := wshub.NewClient(hub, conn)
		hub.Register(c)
		go c.WritePump()
		go c.ReadPump()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := d.Dial(url, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	return conn
}

func newRunner(hub *wshub.Hub, dirty *Dirty, topic string) *EventRunner {
	return &EventRunner{
		Logger: logx.WithContext(context.Background()),
		topic:  topic, group: "test", hub: hub, dirty: dirty,
	}
}

// TestEventRunner_Handle_Broadcast 事件到达时透传原始消息体并广播给在线大屏.
func TestEventRunner_Handle_Broadcast(t *testing.T) {
	hub := wshub.NewHub()
	dirty := NewDirty()
	url := startTestWS(t, hub)

	conn := dial(t, url)
	defer conn.Close()
	waitFor(t, func() bool { return hub.Count() == 1 })

	raw := `{"alarm_id":"A-1","level":4,"device_id":"dev-1"}`
	newRunner(hub, dirty, kafka.TopicAlarm).handle(kafka.Message{Value: []byte(raw)})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("未收到事件广播: %v", err)
	}

	var got eventMsg
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("消息不是合法 JSON: %v (%s)", err, msg)
	}
	if got.Type != "event" {
		t.Errorf("type = %q, 期望 event", got.Type)
	}
	if got.Source != "alarm" {
		t.Errorf("source = %q, 期望 alarm", got.Source)
	}
	if got.Topic != kafka.TopicAlarm {
		t.Errorf("topic = %q, 期望 %q", got.Topic, kafka.TopicAlarm)
	}
	// 透传校验: 本服务不解析上游 schema, 原始字段必须原样到达前端
	if string(got.Data) != raw {
		t.Errorf("data 未原样透传: %s", got.Data)
	}
	if got.ReceivedAt == 0 {
		t.Error("received_at 应被填充")
	}
}

// TestEventRunner_Handle_MarksDirtyWithoutClients 没有大屏在线时也必须置脏.
// 否则"没人看就不失效缓存", HTTP 的 /overview 会一直返回旧值。
func TestEventRunner_Handle_MarksDirtyWithoutClients(t *testing.T) {
	hub := wshub.NewHub()
	dirty := NewDirty()

	newRunner(hub, dirty, kafka.TopicWorkorder).handle(kafka.Message{Value: []byte(`{"id":1}`)})

	if !dirty.Take() {
		t.Error("无在线大屏时事件仍应置脏(HTTP /overview 同样吃这份缓存)")
	}
}

// TestEventRunner_Handle_EmptyPayloadStillMarks 空消息体也要置脏 ——
// 上游只发了个心跳类空事件, 一样可能意味着数据变了, 不能因此丢掉缓存失效。
func TestEventRunner_Handle_EmptyPayloadStillMarks(t *testing.T) {
	hub := wshub.NewHub()
	dirty := NewDirty()

	newRunner(hub, dirty, kafka.TopicAlarm).handle(kafka.Message{Value: nil})

	if !dirty.Take() {
		t.Error("空消息体也应置脏")
	}
}
