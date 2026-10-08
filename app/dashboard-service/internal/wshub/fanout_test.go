package wshub

import (
	"context"
	"os"
	"testing"
	"time"

	"onepark/common/redisx"

	"github.com/gorilla/websocket"
)

// fakePublisher 记录被发布的原始消息(不接 Redis), 用于验证"发布时机"。
type fakePublisher struct {
	got chan []byte
}

func (f *fakePublisher) Publish(_ context.Context, msg []byte) {
	select {
	case f.got <- msg:
	default:
	}
}

// TestHub_BroadcastPublishesButDeliverLocalDoesNot 钉住两条路径的分工:
//   - Broadcast    = 本地投递 + 发布(给其它实例)
//   - DeliverLocal = 只本地投递, **不发布**
//
// 第二条是防回环的关键: 订阅端收到别的实例的消息后必须用 DeliverLocal,
// 若用 Broadcast, 消息会 A->B->A->B 无限反弹, 大屏上表现为同一条告警刷屏。
func TestHub_BroadcastPublishesButDeliverLocalDoesNot(t *testing.T) {
	hub := NewHub()
	pub := &fakePublisher{got: make(chan []byte, 4)}
	hub.SetPublisher(context.Background(), pub)

	hub.Broadcast([]byte(`{"type":"snapshot"}`))
	select {
	case <-pub.got:
	case <-time.After(time.Second):
		t.Fatal("Broadcast 应当把消息发布给其它实例")
	}

	hub.DeliverLocal([]byte(`{"type":"snapshot"}`))
	select {
	case <-pub.got:
		t.Fatal("DeliverLocal 不应发布 —— 否则实例之间会来回反弹")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHub_NoPublisherIsLocalOnly 单实例(未注入发布器)时不能 panic、且不影响本地投递。
func TestHub_NoPublisherIsLocalOnly(t *testing.T) {
	hub := NewHub()
	hub.Broadcast([]byte(`{"type":"snapshot"}`)) // 无 publisher: 纯本地, 不应 panic
	hub.DeliverLocal([]byte(`{"type":"event"}`))
	if hub.Count() != 0 {
		t.Fatalf("没有连接时 Count 应为 0, 实际 %d", hub.Count())
	}
}

// TestRedisFanout_CrossInstanceExactlyOnce 真实 Redis 上的跨实例扇出:
//
//	实例 A 广播 -> A 的客户端收到 1 条(本地投递)
//	            -> B 的客户端收到 1 条(经 Redis Pub/Sub 转发)
//	            -> A **不能**因为自己的订阅回放再收到一条(自回环必须丢弃)
//
// 最后一条是本用例的核心: 少了它会**静默多推一倍消息**, 前端看起来只是"数据跳变"。
func TestRedisFanout_CrossInstanceExactlyOnce(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6380"
	}
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: addr, Pass: os.Getenv("REDIS_PASS")})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过: Redis 不可用(%s): %v", addr, err)
	}

	channel := "onepark:test:ws:fanout:" + time.Now().Format("150405.000000")

	hubA, hubB := NewHub(), NewHub()
	fanA := NewRedisFanout(rdb, channel)
	fanB := NewRedisFanout(rdb, channel)
	hubA.SetPublisher(ctx, fanA)
	hubB.SetPublisher(ctx, fanB)

	go fanA.Run(ctx, hubA)
	go fanB.Run(ctx, hubB)
	// 订阅建立是异步的: 等一小会儿再广播, 否则稳定地漏第一条(测试假失败)
	time.Sleep(300 * time.Millisecond)

	urlA := startTestWS(t, hubA)
	urlB := startTestWS(t, hubB)
	connA := dial(t, urlA)
	connB := dial(t, urlB)
	t.Cleanup(func() {
		_ = connA.Close()
		_ = connB.Close()
	})

	waitFor(t, func() bool { return hubA.Count() == 1 && hubB.Count() == 1 })

	payload := []byte(`{"type":"event","source":"test"}`)
	hubA.Broadcast(payload)

	if got := readMsg(t, connA, 2*time.Second); string(got) != string(payload) {
		t.Fatalf("A 实例本地投递内容不对: %s", string(got))
	}
	if got := readMsg(t, connB, 2*time.Second); string(got) != string(payload) {
		t.Fatalf("B 实例未收到跨实例消息(或内容不对): %s", string(got))
	}

	// A 不应收到第二条: 自己的消息经订阅回放再投递一次, 就是自回环 bug
	_ = connA.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	if _, msg, err := connA.ReadMessage(); err == nil {
		t.Fatalf("A 实例收到重复消息(自回环未丢弃): %s", string(msg))
	}
}

// readMsg 读一条消息, 超时即 Fatal。
func readMsg(t *testing.T, conn *websocket.Conn, timeout time.Duration) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读消息失败(超时 %s): %v", timeout, err)
	}
	return msg
}
