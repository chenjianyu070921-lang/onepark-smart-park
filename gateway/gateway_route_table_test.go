package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"onepark/gateway/internal/config"
	"onepark/gateway/internal/proxy"

	"github.com/zeromicro/go-zero/core/conf"
)

// expectedPrefixes 路由表必须满足的前缀集合(来源 etc/apigateway-api.yaml).
// 用于捕获"漏配某服务"的回归: 任一缺失即测试失败.
var expectedPrefixes = []string{
	"/api/workorder", "/api/visitor", "/api/parking", "/api/notice", "/api/lease",
	"/api/dashboard", "/api/dispatch", "/api/auth", "/api/users", "/api/roles",
	"/api/menus", "/api/permissions", "/api/devices", "/api/device", "/api/product",
	"/api/alarm", "/api/access", "/api/video",
	"/api/energy/daily", "/api/energy/monthly", "/api/energy/zone", "/api/energy",
	"/api/billing", "/ws/alarm", "/ws/dashboard",
}

// recordingBackend 在随机端口起回显服务, 记录收到的请求路径以判定命中了哪条路由.
type recordingBackend struct {
	mu     sync.Mutex
	prefix string
	addr   string
	hits   []string
	srv    *http.Server
}

func newRecordingBackend(t *testing.T, prefix string) *recordingBackend {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &recordingBackend{prefix: prefix, addr: l.Addr().String()}
	b.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.hits = append(b.hits, r.URL.Path)
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"0","msg":"ok"}`))
	})}
	go func() { _ = b.srv.Serve(l) }()
	t.Cleanup(func() { _ = b.srv.Close() })
	return b
}

func (b *recordingBackend) got(path string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, h := range b.hits {
		if h == path {
			return true
		}
	}
	return false
}

// TestRouteTableRuntime 运行时验证: 加载真实 etc/apigateway-api.yaml, 用独立 mock 后端替换每条上游,
// 断言 (1) 配置合法性/完整性; (2) 每个前缀都能正确转发到对应后端(不 404/502); (3) 未知前缀返回 404.
// 这是对"统一网关路由表已修复"的端到端回归保护.
func TestRouteTableRuntime(t *testing.T) {
	// go-zero 的 UseEnv 基于 os.ExpandEnv, 不支持 ${VAR:-default} 语法,
	// 故 GATEWAY_MODE 未注入时会被展开为""导致 Mode 枚举校验失败;
	// 测试显式注入(与 compose/.env 行为一致), 使真实配置可被加载.
	t.Setenv("GATEWAY_MODE", "dev")

	// 1) 加载真实配置(校验 yaml 合法性 + 环境变量展开).
	var c config.Config
	conf.MustLoad("etc/apigateway-api.yaml", &c, conf.UseEnv())

	// 2) 完整性: 真实配置必须包含全部预期前缀.
	got := make(map[string]bool, len(c.Upstreams))
	for _, u := range c.Upstreams {
		got[u.Prefix] = true
	}
	for _, p := range expectedPrefixes {
		if !got[p] {
			t.Fatalf("路由表缺失预期前缀: %s", p)
		}
	}

	// 3) 每条上游替换为独立 mock 后端(端口随机), 精确判定命中.
	backends := make(map[string]*recordingBackend, len(c.Upstreams))
	for _, u := range c.Upstreams {
		backends[u.Prefix] = newRecordingBackend(t, u.Prefix)
	}
	ups := make([]config.UpstreamConf, len(c.Upstreams))
	for i, u := range c.Upstreams {
		ups[i] = config.UpstreamConf{Prefix: u.Prefix, Target: "http://" + backends[u.Prefix].addr}
	}
	gw, err := proxy.NewGateway(config.Config{Upstreams: ups})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// 4) 每个前缀发探针, 断言命中自身后端(转发成功且路径透传).
	for _, u := range c.Upstreams {
		probe := u.Prefix + "/probe"
		req := httptest.NewRequest(http.MethodGet, probe, nil)
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("前缀 %s 转发状态=%d, want 200", u.Prefix, rec.Code)
		}
		if !backends[u.Prefix].got(probe) {
			t.Fatalf("前缀 %s 未正确转发到对应后端", u.Prefix)
		}
	}

	// 5) 未知前缀必须返回 404(统一错误体).
	req := httptest.NewRequest(http.MethodGet, "/api/this-service-does-not-exist/x", nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知前缀状态=%d, want 404", rec.Code)
	}
}

// TestRouteTableLongestPrefix 显式校验最长前缀优先匹配(路由表核心不变量):
// /api/devices 不能误命中 /api/device; /api/energy/daily 不能误命中 /api/energy.
func TestRouteTableLongestPrefix(t *testing.T) {
	device := newRecordingBackend(t, "/api/device")
	devices := newRecordingBackend(t, "/api/devices")
	energy := newRecordingBackend(t, "/api/energy")
	daily := newRecordingBackend(t, "/api/energy/daily")

	gw, err := proxy.NewGateway(config.Config{Upstreams: []config.UpstreamConf{
		{Prefix: "/api/device", Target: "http://" + device.addr},
		{Prefix: "/api/devices", Target: "http://" + devices.addr},
		{Prefix: "/api/energy", Target: "http://" + energy.addr},
		{Prefix: "/api/energy/daily", Target: "http://" + daily.addr},
	}})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	cases := []struct {
		path string
		want *recordingBackend
	}{
		{"/api/device/x", device},
		{"/api/devices/x", devices}, // 必须命中 /api/devices 而非 /api/device
		{"/api/energy/x", energy},
		{"/api/energy/daily/x", daily}, // 必须命中 /api/energy/daily 而非 /api/energy
	}
	for _, cs := range cases {
		req := httptest.NewRequest(http.MethodGet, cs.path, nil)
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s -> 状态 %d, want 200", cs.path, rec.Code)
		}
		if !cs.want.got(cs.path) {
			t.Fatalf("%s 误命中其他路由(期望前缀 %s)", cs.path, cs.want.prefix)
		}
	}
}
