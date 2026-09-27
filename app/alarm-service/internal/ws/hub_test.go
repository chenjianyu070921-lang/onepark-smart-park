package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 测试替身: 不依赖真实 TCP 连接
// ---------------------------------------------------------------------------

// fakeConn 记录写入的消息, 并可控地让写操作失败(模拟半开连接).
type fakeConn struct {
	mu       sync.Mutex
	written  [][]byte
	closed   bool
	writeErr error
	readErr  error
	// readBlock 让 ReadMessage 一直阻塞, 模拟长连接不主动断开.
	readBlock chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{readBlock: make(chan struct{})}
}

func (f *fakeConn) SetReadLimit(int64)                          {}
func (f *fakeConn) SetReadDeadline(time.Time) error             { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error            { return nil }
func (f *fakeConn) ReadMessage() (int, []byte, error) {
	if f.readErr != nil {
		return 0, nil, f.readErr
	}
	<-f.readBlock // 阻塞直到测试结束, 模拟连接保持
	return 0, nil, errors.New("closed")
}
func (f *fakeConn) WriteControl(int, []byte, time.Time) error { return nil }

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, append([]byte(nil), data...))
	return nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	select {
	case <-f.readBlock:
	default:
		close(f.readBlock)
	}
	return nil
}

func (f *fakeConn) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.written))
	for _, w := range f.written {
		out = append(out, string(w))
	}
	return out
}

// addClient 向 hub 注册一个替身连接.
// 故意不启动 readPump/writePump: 广播是"投递到 send 通道", 测试直接读通道即可断言,
// 不启动写泵可避免并发与时序干扰(写泵的下发行为由 writePump 自身保证).
func addClient(t *testing.T, hub *Hub, tenantID int64) *Client {
	t.Helper()
	c := newClient(hub, newFakeConn(), tenantID)
	hub.mu.Lock()
	hub.clients[c] = struct{}{}
	hub.mu.Unlock()
	return c
}

// recv 从连接的发送通道取一条消息(带超时), 取不到返回 false.
func recv(c *Client, timeout time.Duration) ([]byte, bool) {
	select {
	case msg := <-c.send:
		return msg, true
	case <-time.After(timeout):
		return nil, false
	}
}

// waitCount 等待在线连接数达到期望值(踢除是异步的, 需要轮询而非立即断言).
func waitCount(hub *Hub, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if hub.Count() == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return hub.Count() == want
}

// ---------------------------------------------------------------------------
// 广播与租户隔离
// ---------------------------------------------------------------------------

// TestHub_BroadcastToTenantIsolated 告警绝不能跨园区推送.
func TestHub_BroadcastToTenantIsolated(t *testing.T) {
	hub := NewHub()
	a := addClient(t, hub, 1)
	b := addClient(t, hub, 2)

	hub.BroadcastTo(1, []byte("hello-1"))

	msg, ok := recv(a, time.Second)
	if !ok || string(msg) != "hello-1" {
		t.Errorf("园区1的连接应收到消息: msg=%s ok=%v", msg, ok)
	}
	if msg, ok := recv(b, 100*time.Millisecond); ok {
		t.Errorf("园区2的连接不应收到园区1的消息: %s", msg)
	}
}

// TestHub_BroadcastToAll tenantID<=0 时广播给全部连接(内部运维用途).
func TestHub_BroadcastToAll(t *testing.T) {
	hub := NewHub()
	a := addClient(t, hub, 1)
	b := addClient(t, hub, 2)

	hub.BroadcastTo(0, []byte("notice"))

	if _, ok := recv(a, time.Second); !ok {
		t.Error("tenantID=0 应广播给所有连接: a 未收到")
	}
	if _, ok := recv(b, time.Second); !ok {
		t.Error("tenantID=0 应广播给所有连接: b 未收到")
	}
}

// TestHub_UnregisterIdempotent 重复注销不能 panic(关闭已关闭的通道会 panic).
func TestHub_UnregisterIdempotent(t *testing.T) {
	hub := NewHub()
	c := addClient(t, hub, 1)

	hub.Unregister(c)
	hub.Unregister(c) // 第二次必须安全
	if hub.Count() != 0 {
		t.Errorf("注销后应无连接, 实际 %d", hub.Count())
	}
}

// TestHub_SlowClientKicked 缓冲满的慢消费者被踢除, 且不阻塞其余连接.
func TestHub_SlowClientKicked(t *testing.T) {
	hub := NewHub()
	slow := addClient(t, hub, 1)
	normal := addClient(t, hub, 1)

	// 填满慢消费者的缓冲(不消费).
	for i := 0; i < sendBufferSize; i++ {
		slow.send <- []byte("x")
	}

	done := make(chan struct{})
	go func() {
		hub.BroadcastTo(1, []byte("new"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("广播被慢消费者阻塞")
	}

	// 慢消费者被移出注册表(异步踢除, 轮询等待), 正常连接仍在线并收到消息.
	if !waitCount(hub, 1, 2*time.Second) {
		t.Errorf("慢消费者应被踢除, 剩余连接数=%d", hub.Count())
	}
	if _, ok := recv(normal, time.Second); !ok {
		t.Error("正常连接应收到消息")
	}
}

// TestHub_Push 信封序列化与投递; 无在线连接时返回 false 且不报错.
func TestHub_Push(t *testing.T) {
	hub := NewHub()
	c := addClient(t, hub, 7)

	ok := hub.Push(7, NewEnvelope(TypeAlarmCreated, AlarmEvent{AlarmID: "AL-20260922-0001", DeviceID: "door-01", Level: 3}, "rid-1"))
	if !ok {
		t.Fatal("有在线连接时 Push 应返回 true")
	}
	msg, ok := recv(c, time.Second)
	if !ok {
		t.Fatal("有在线连接时应收到消息")
	}

	var env Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("消息不是合法 JSON: %v", err)
	}
	if env.Type != TypeAlarmCreated || env.RequestId != "rid-1" || env.Ts == 0 {
		t.Errorf("信封字段异常: %+v", env)
	}
	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data 应为对象, 实际 %T", env.Data)
	}
	if data["device_id"] != "door-01" {
		t.Errorf("data 内容异常: %+v", data)
	}

	// 无在线连接的园区: 不报错, 返回 false.
	empty := NewHub()
	if empty.Push(99, NewEnvelope(TypeAlarmAck, AlarmEvent{}, "")) {
		t.Error("无在线连接时不应返回 true")
	}
	// nil 安全.
	var nilHub *Hub
	if nilHub.Push(1, NewEnvelope(TypeAlarmAck, AlarmEvent{}, "")) {
		t.Error("nil Hub 推送应安全返回 false")
	}
}

// TestEnvelope_Encode 序列化必须能真正产出 JSON(曾因方法命名为 MarshalJSON 导致递归).
func TestEnvelope_Encode(t *testing.T) {
	raw, err := NewEnvelope(TypeAlarmResolved, AlarmEvent{AlarmID: "AL-20260922-0009"}, "r").Encode()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"alarm.resolved"`) {
		t.Errorf("序列化结果异常: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Handler: 握手前的租户校验
// ---------------------------------------------------------------------------

func TestHandler_RejectsInvalidTenant(t *testing.T) {
	hub := NewHub()
	handler := hub.Handler()

	cases := map[string]string{
		"缺少租户":  "/ws/alarm",
		"租户非数字": "/ws/alarm?tenant_id=abc",
		"租户为0":   "/ws/alarm?tenant_id=0",
		"租户负数":   "/ws/alarm?tenant_id=-1",
	}
	for name, target := range cases {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: 期望 400, 实际 %d", name, rec.Code)
		}
	}
	if hub.Count() != 0 {
		t.Error("非法请求不应产生连接")
	}
}
