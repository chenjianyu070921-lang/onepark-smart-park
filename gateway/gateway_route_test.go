package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"onepark/common/redisx"
	"onepark/gateway/internal/config"
	"onepark/gateway/internal/middleware"
	"onepark/gateway/internal/proxy"
)

// mockUpstream 在随机端口起一个回显服务, 返回 200 + 固定 JSON, 模拟业务服务.
func mockUpstream(t *testing.T) (port int, closeFn func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"0","msg":"ok","data":{"hit":true}}`))
	})}
	go func() { _ = srv.Serve(l) }()
	return l.Addr().(*net.TCPAddr).Port, func() { _ = srv.Close() }
}

// TestProxyForward 校验按 /api/<svc> 前缀转发到对应 upstream 并透传响应.
func TestProxyForward(t *testing.T) {
	port, closeFn := mockUpstream(t)
	defer closeFn()
	cfg := config.Config{Upstreams: []config.UpstreamConf{{Prefix: "/api/devices", Target: "http://127.0.0.1:" + strconv.Itoa(port)}}}
	h, err := proxy.NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/devices/123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("forward status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"hit":true`) {
		t.Errorf("forward body not proxied: %s", rec.Body.String())
	}
}

// TestProxyNotFound 未匹配任何服务前缀应返回 errorx 风格的 404.
func TestProxyNotFound(t *testing.T) {
	h, err := proxy.NewGateway(config.Config{})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "M6-E-0004") {
		t.Errorf("body should contain ErrNotFound code, got %s", rec.Body.String())
	}
}

// TestProxyBadGateway upstream 配置了但无服务监听(连接拒绝)应返回 errorx 风格的 502.
func TestProxyBadGateway(t *testing.T) {
	cfg := config.Config{Upstreams: []config.UpstreamConf{{Prefix: "/api/devices", Target: "http://127.0.0.1:1"}}}
	h, err := proxy.NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "M6-E-0006") {
		t.Errorf("body should contain ErrBadGateway code, got %s", rec.Body.String())
	}
}

// TestRateLimit 基于 miniredis 校验令牌桶限流: 容量 2, 第 3 次请求应被限流返回 429.
func TestRateLimit(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: mr.Addr()})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.RateLimit(rdb, 2, 0.1)(next)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "127.0.0.1:5555" // 固定客户端 IP, 共享同一限流键
		rec := httptest.NewRecorder()
		h(rec, req)
		switch {
		case i < 2 && rec.Code != http.StatusOK:
			t.Fatalf("req %d status = %d, want 200", i, rec.Code)
		case i == 2 && rec.Code != http.StatusTooManyRequests:
			t.Fatalf("req %d status = %d, want 429", i, rec.Code)
		case i == 2 && !strings.Contains(rec.Body.String(), "M6-E-0007"):
			t.Errorf("body should contain ErrRateLimited code, got %s", rec.Body.String())
		}
	}
}
