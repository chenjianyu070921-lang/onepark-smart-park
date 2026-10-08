package wsserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"onepark/app/dashboard-service/internal/wshub"
	"onepark/common/jwt"
)

// TestOriginChecker 覆盖判定规则本身(纯函数, 不起连接)。
//
// 重点看两处容易误解的行为:
//   - **无 Origin 放行**: 压测工具/服务端订阅不带 Origin, 而 CSRF 的前提是"浏览器自动带凭据";
//   - **留空不等于放开**: 未配置白名单时, 跨站 Origin 必须被拒(这是本次修复的核心)。
func TestOriginChecker(t *testing.T) {
	cases := []struct {
		name    string
		allow   []string
		origin  string
		host    string
		wantOK  bool
		comment string
	}{
		{"无 Origin 放行(非浏览器客户端)", nil, "", "dash.example.com", true, "压测/服务端订阅"},
		{"同源放行", nil, "https://dash.example.com", "dash.example.com", true, "与请求 Host 相同"},
		{"同源不同 scheme 放行", nil, "https://dash.example.com", "dash.example.com", true, "反代下 scheme 会变, 只比 host"},
		{"未配置白名单时跨站拒绝", nil, "https://evil.example.com", "dash.example.com", false, "★ 本次修复的核心: 留空≠放开"},
		{"命中白名单", []string{"http://localhost:5173"}, "http://localhost:5173", "dash.example.com", true, ""},
		{"白名单忽略了末尾斜杠", []string{"http://localhost:5173/"}, "http://localhost:5173", "dash.example.com", true, "配置里多个斜杠不该失效"},
		{"白名单大小写不敏感", []string{"HTTP://LocalHost:5173"}, "http://localhost:5173", "dash.example.com", true, ""},
		{"白名单不含端口则拒绝带端口的来源", []string{"http://localhost"}, "http://localhost:5173", "dash.example.com", false, "host 含端口, 精确比对"},
		{"不在白名单拒绝", []string{"http://localhost:5173"}, "https://evil.example.com", "dash.example.com", false, ""},
		{"通配符放行一切", []string{"*"}, "https://evil.example.com", "dash.example.com", true, "仅供本机联调"},
		{"非法 Origin 拒绝", nil, "not-a-url", "dash.example.com", false, "解析失败不能默认放行"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := OriginChecker(tc.allow)
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/ws/dashboard", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := check(r); got != tc.wantOK {
				t.Fatalf("allow=%v origin=%q host=%q -> got %v, want %v (%s)",
					tc.allow, tc.origin, tc.host, got, tc.wantOK, tc.comment)
			}
		})
	}
}

// TestHandler_HandshakeOrigin 走**真实握手**: 证明校验真的接在路由上, 而不只是那个纯函数对。
// 只测纯函数的话, 有人把 Handler 里的 CheckOrigin 改回 `return true` 也不会有测试报警。
//
// token 走 jwt.Generate 现签; 中间件未注入身份时 Handler 才会走 ?token= 分支(直连场景)。
func TestHandler_HandshakeOrigin(t *testing.T) {
	const secret = "origin-test-secret"
	token, err := jwt.Generate(secret, 1, "", 1, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}

	hub := wshub.NewHub()
	// 白名单只放行 localhost:5173; 其它来源应被拒
	srv := httptest.NewServer(Handler(hub, secret, []string{"http://localhost:5173"}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=" + token

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}

	// 1) 白名单内的来源 -> 握手成功
	hdr := http.Header{}
	hdr.Set("Origin", "http://localhost:5173")
	conn, _, err := dialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("白名单内来源应握手成功, 实际失败: %v", err)
	}
	_ = conn.Close()

	// 2) 白名单外来源 -> 握手失败, 且 HTTP 状态为 403(Upgrader 拒绝的固定表现)
	hdr2 := http.Header{}
	hdr2.Set("Origin", "https://evil.example.com")
	conn2, resp2, err := dialer.Dial(wsURL, hdr2)
	if err == nil {
		_ = conn2.Close()
		t.Fatal("跨站来源竟然握手成功了 —— CheckOrigin 没生效")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusForbidden {
		got := 0
		if resp2 != nil {
			got = resp2.StatusCode
		}
		t.Fatalf("跨站来源应以 403 拒绝, 实际状态=%d err=%v", got, err)
	}
}

// TestHandler_AllowsNoOrigin 非浏览器客户端(不带 Origin)必须仍能接入 ——
// 压测工具与联调脚本都属这一类, 若被一起挡掉会以为是"鉴权坏了"。
func TestHandler_AllowsNoOrigin(t *testing.T) {
	const secret = "origin-test-secret"
	token, err := jwt.Generate(secret, 1, "", 1, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}

	hub := wshub.NewHub()
	srv := httptest.NewServer(Handler(hub, secret, nil))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=" + token

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil) // 不带 Origin
	if err != nil {
		t.Fatalf("无 Origin 的客户端应可接入, 实际失败: %v", err)
	}
	defer conn.Close()

	// 顺带确认连接真的注册进了 Hub(而不是只完成握手)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Count() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("连接已握手但未注册进 Hub: count=%d", hub.Count())
}
