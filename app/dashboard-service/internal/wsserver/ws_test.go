package wsserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/wshub"
)

// 本文件覆盖 ws.go —— 大屏 WebSocket 的**入口**。
// 此前整个文件 0% 覆盖, 而它是演示路径: 前端连不上, 大屏就是一块黑屏。

// waitCount 轮询等待在线数达到期望值(注册/注销是异步的, 不能立刻断言).
func waitCount(hub *wshub.Hub, want int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if hub.Count() == want {
			return want
		}
		time.Sleep(10 * time.Millisecond)
	}
	return hub.Count()
}

// TestHandler_UpgradeRegisterAndBroadcast 大屏接入 -> 注册 -> 收到广播 -> 断开回落.
func TestHandler_UpgradeRegisterAndBroadcast(t *testing.T) {
	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 拨号失败: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("升级状态码 = %d, 期望 101(Switching Protocols)", resp.StatusCode)
	}

	// 接入后必须出现在在线列表里
	if got := waitCount(hub, 1, 2*time.Second); got != 1 {
		t.Fatalf("接入后在线客户端数 = %d, 期望 1", got)
	}

	// 服务端广播必须能被大屏收到(推链路真正打通的证据)
	payload := []byte(`{"type":"snapshot","data":null}`)
	hub.Broadcast(payload)
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("设置读超时失败: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取广播失败: %v", err)
	}
	if string(msg) != string(payload) {
		t.Errorf("收到的消息 = %s, 期望 %s", msg, payload)
	}

	// 断开后在线数必须回落 —— 不回落就是连接泄漏, 大屏会越连越多
	_ = conn.Close()
	if got := waitCount(hub, 0, 3*time.Second); got != 0 {
		t.Errorf("断开后在线客户端数 = %d, 期望 0(连接未注销)", got)
	}
}

// TestHandler_TokenIsCurrentlyIgnored 记录当前鉴权状态: `?token=` 尚未校验.
//
// ⚠️ 这是**刻意钉住的已知缺口**而不是正确行为 —— 确认书已披露「M6 JWT 网关就绪后必须在
// Handler 中补校验」。本用例的作用是: 一旦补上校验, 它会失败并提醒同步更新文档与前端,
// 而不是让这个安全性变化静默发生。
func TestHandler_TokenIsCurrentlyIgnored(t *testing.T) {
	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=definitely-invalid"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("开发期 token 未校验, 任意 token 都应能接入;"+
			"若此处失败说明已补上校验 —— 请同步更新确认书与前端: %v", err)
	}
	_ = conn.Close()
}

// TestStartSnapshotPush_StopsOnContextCancel 上下文取消后阻塞循环必须退出.
//
// 5 秒一条的快照循环若不能退出, 服务优雅停机时就会泄漏 goroutine。
// (循环体本身需要完整 svcCtx 与真实上游, 属于端到端范畴, 此处只锁"能停下"。
//  同时覆盖 dirty=nil 的兜底分支 —— 未启用事件推送时调用方无需判空。)
func TestStartSnapshotPush_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消: 循环应一次都不跑就返回

	done := make(chan struct{})
	go func() {
		StartSnapshotPush(ctx, &svc.ServiceContext{}, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("上下文已取消但快照推送未退出, 会泄漏 goroutine")
	}
}
