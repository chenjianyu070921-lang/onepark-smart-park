package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onepark/gateway/internal/config"
)

// 起两个上游桩, 验证网关按最长前缀匹配转发, 且请求路径被原样透传.
func TestRouteForwarding(t *testing.T) {
	workorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "workorder")
		_, _ = w.Write([]byte("WO:" + r.URL.Path))
	}))
	defer workorder.Close()

	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "device")
		_, _ = w.Write([]byte("DEV:" + r.URL.Path))
	}))
	defer device.Close()

	cfg := config.Config{
		Upstreams: []config.UpstreamConf{
			{Prefix: "/api/device", Target: device.URL},
			{Prefix: "/api/workorder", Target: workorder.URL},
		},
	}
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		wantUp     string // 期望命中的上游 X-Upstream 值; 空表示期望 404
		wantBody   string // body 前缀
		wantStatus int
	}{
		// 验收关键: /api/workorders 必须匹配 /api/workorder 前缀, 打到 M2(workorder-service).
		{"workorder detail", "/api/workorder/123", "workorder", "WO:/api/workorder/123", http.StatusOK},
		{"workorder plural", "/api/workorders", "workorder", "WO:/api/workorders", http.StatusOK},
		{"device prefix", "/api/device/abc", "device", "DEV:/api/device/abc", http.StatusOK},
		{"no route", "/api/unknown/x", "", "", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			g.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status want %d, got %d (body=%s)", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantUp != "" {
				if got := rec.Header().Get("X-Upstream"); got != tc.wantUp {
					t.Errorf("upstream want %q, got %q", tc.wantUp, got)
				}
				if !strings.HasPrefix(rec.Body.String(), tc.wantBody) {
					t.Errorf("body want prefix %q, got %q", tc.wantBody, rec.Body.String())
				}
			}
		})
	}
}

// TestLongestPrefixWins 验证更长前缀优先(如 /api/workorder/sub 不应被 /api/workorder 抢走).
func TestLongestPrefixWins(t *testing.T) {
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("SHORT"))
	}))
	defer short.Close()
	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("LONG"))
	}))
	defer long.Close()

	cfg := config.Config{
		Upstreams: []config.UpstreamConf{
			{Prefix: "/api/workorder", Target: short.URL},
			{Prefix: "/api/workorder/sub", Target: long.URL},
		},
	}
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorder/sub/x", nil)
	g.ServeHTTP(rec, req)
	if rec.Body.String() != "LONG" {
		t.Fatalf("expected longest prefix 'LONG', got %q", rec.Body.String())
	}
}

// TestCORSPreflight 验证 OPTIONS 预检直接返回 204, 不下游.
func TestCORSPreflight(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("SHOULD-NOT-REACH"))
	}))
	defer up.Close()

	cfg := config.Config{
		Upstreams: []config.UpstreamConf{{Prefix: "/api/workorder", Target: up.URL}},
	}
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/workorder/123", nil)
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS want 204, got %d", rec.Code)
	}
	if rec.Body.String() != "" {
		t.Fatalf("OPTIONS body should be empty, got %q", rec.Body.String())
	}
}
