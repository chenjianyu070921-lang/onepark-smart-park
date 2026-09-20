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
	"onepark/common/jwt"
)

// 本文件覆盖 ws.go —— 大屏 WebSocket 的**入口**。
//
// ⚠️ 2026-09-20 合并 develop 后**重写过一次**：A9（WS 鉴权）已由平台侧落地，
// `Handler` 从 `(hub)` 变为 `(hub, jwtSecret)` 并真的校验 token。
// 老弟原来那条「钉住 `?token=` 未校验」的用例随之作废 —— 它本来就是为了
// 「一旦补上校验就让用例失败并提醒同步文档」，现在正好兑现了。

const wsTestSecret = "test-ws-secret-for-unit-test"

// dialWS 尝试握手, 返回连接与响应(失败时连接为 nil, 响应仍可读状态码).
func dialWS(t *testing.T, srv *httptest.Server, query string) (*websocket.Conn, *http.Response) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard" + query
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		// 非 101 时 gorilla 返回 ErrBadHandshake, 但 resp 里有状态码
		return nil, resp
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, resp
}

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

// TestHandler_RefusesWhenSecretUnconfigured **fail-closed**: 未配置密钥时拒绝接入, 而不是放行.
//
// 这是"大屏裸奔"的兜底 —— 若此处退化成放行, 任何人不带凭证就能连上大屏拿到园区全量数据。
func TestHandler_RefusesWhenSecretUnconfigured(t *testing.T) {
	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub, ""))
	t.Cleanup(srv.Close)

	conn, resp := dialWS(t, srv, "")
	if conn != nil {
		t.Fatal("未配置 jwt secret 时不应允许接入")
	}
	if resp == nil {
		t.Fatal("应返回 HTTP 响应")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("状态码 = %d, 期望 500(服务端配置缺失, 明确失败而非静默放行)", resp.StatusCode)
	}
	if hub.Count() != 0 {
		t.Errorf("被拒的连接不应注册进 hub, 实际在线 %d", hub.Count())
	}
}

// TestHandler_RejectsBadTokens 缺 token / 假 token / refresh token 一律 401.
func TestHandler_RejectsBadTokens(t *testing.T) {
	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub, wsTestSecret))
	t.Cleanup(srv.Close)

	refresh, err := jwt.Generate(wsTestSecret, 1001, "1", 1, jwt.TypeRefresh, 3600)
	if err != nil {
		t.Fatalf("构造 refresh token 失败: %v", err)
	}
	wrongSecret, err := jwt.Generate("another-secret", 1001, "1", 1, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("构造异密钥 token 失败: %v", err)
	}

	cases := []struct {
		name  string
		query string
	}{
		{"完全不带 token", ""},
		{"token 为空串", "?token="},
		{"token 是垃圾串", "?token=not-a-jwt"},
		{"用别的密钥签的 token", "?token=" + wrongSecret},
		{"refresh token 不允许接入", "?token=" + refresh},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, resp := dialWS(t, srv, c.query)
			if conn != nil {
				t.Fatal("应拒绝接入, 但握手成功了")
			}
			if resp == nil {
				t.Fatal("应返回 HTTP 响应")
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("状态码 = %d, 期望 401", resp.StatusCode)
			}
		})
	}

	if hub.Count() != 0 {
		t.Errorf("全部被拒, 在线数应为 0, 实际 %d", hub.Count())
	}
}

// TestHandler_ValidTokenUpgradesRegistersAndBroadcasts 合法 access token: 升级成功 ->
// 注册进 hub -> 收到广播 -> 断开后在线数回落.
func TestHandler_ValidTokenUpgradesRegistersAndBroadcasts(t *testing.T) {
	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub, wsTestSecret))
	t.Cleanup(srv.Close)

	token, err := jwt.Generate(wsTestSecret, 1001, "1", 1, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("构造 access token 失败: %v", err)
	}

	conn, resp := dialWS(t, srv, "?token="+token)
	if conn == nil {
		t.Fatalf("合法 token 应能接入, 状态码: %v", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("升级状态码 = %d, 期望 101", resp.StatusCode)
	}

	if got := waitCount(hub, 1, 2*time.Second); got != 1 {
		t.Fatalf("接入后在线客户端数 = %d, 期望 1", got)
	}

	// 推链路真正打通的证据: 服务端广播必须能被大屏收到
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

	// 断开后必须回落 —— 不回落就是连接泄漏, 大屏会越连越多
	_ = conn.Close()
	if got := waitCount(hub, 0, 3*time.Second); got != 0 {
		t.Errorf("断开后在线客户端数 = %d, 期望 0(连接未注销)", got)
	}
}

// TestStartSnapshotPush_StopsOnContextCancel 上下文取消后阻塞循环必须退出.
//
// 5 秒一条的快照循环若不能退出, 服务优雅停机时就会泄漏 goroutine。
// (循环体本身需要完整 svcCtx 与真实上游, 属于端到端范畴, 此处只锁"能停下"。
//
//	同时覆盖 dirty=nil 的兜底分支 —— 未启用事件推送时调用方无需判空。)
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
