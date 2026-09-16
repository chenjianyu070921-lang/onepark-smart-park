package wshub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// waitFor 轮询等待条件成立(连接注册/注销都是异步 goroutine, 不能断言瞬时状态).
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

// startTestWS 起一个真实升级 WS 的测试服务器, 返回 ws:// 地址.
func startTestWS(t *testing.T, hub *Hub) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := NewClient(hub, conn)
		hub.Register(c)
		go c.WritePump()
		go c.ReadPump()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// dial 建立 WS 连接.
func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := d.Dial(url, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	return conn
}

// startRawWS 只做协议升级、不注册客户端 —— 用于构造"无 WritePump 的卡死连接".
func startRawWS(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = up.Upgrade(w, r, nil) // 升级后什么都不做, 由测试自己接管连接
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// TestBroadcast_AllClientsReceive 两条大屏同时在线, 广播双方都能收到.
func TestBroadcast_AllClientsReceive(t *testing.T) {
	hub := NewHub()
	url := startTestWS(t, hub)

	c1 := dial(t, url)
	defer c1.Close()
	c2 := dial(t, url)
	defer c2.Close()

	waitFor(t, func() bool { return hub.Count() == 2 })

	hub.Broadcast([]byte(`{"type":"snapshot","data":{"ok":true}}`))

	for name, conn := range map[string]*websocket.Conn{"client1": c1, "client2": c2} {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s 未收到广播: %v", name, err)
		}
		if string(msg) != `{"type":"snapshot","data":{"ok":true}}` {
			t.Errorf("%s 收到 %q, 内容不符", name, msg)
		}
	}
}

// TestUnregister_ClientCountFalls 客户端断开后在线数必须回落(否则连接泄漏).
func TestUnregister_ClientCountFalls(t *testing.T) {
	hub := NewHub()
	url := startTestWS(t, hub)

	conn := dial(t, url)
	waitFor(t, func() bool { return hub.Count() == 1 })

	_ = conn.Close()
	waitFor(t, func() bool { return hub.Count() == 0 })
}

// TestBroadcast_StaleClientKicked 缓冲打满的卡死连接被踢掉, 且不阻塞广播.
func TestBroadcast_StaleClientKicked(t *testing.T) {
	hub := NewHub()
	url := startRawWS(t) // 只升级不注册, 避免服务器 handler 抢先注册干扰计数

	// 建连但不启动 WritePump —— 没人消费发送通道, 缓冲必然打满
	conn := dial(t, url)
	stale := NewClient(hub, conn)
	hub.Register(stale)

	waitFor(t, func() bool { return hub.Count() == 1 })

	// 超过缓冲容量地灌消息: 卡死连接应被异步踢掉, 广播不 panic、不阻塞
	for i := 0; i < sendBufferSize+8; i++ {
		hub.Broadcast([]byte(`{"i":1}`))
	}
	waitFor(t, func() bool { return hub.Count() == 0 })
}
