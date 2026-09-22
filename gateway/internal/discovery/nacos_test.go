package discovery

import (
	"testing"

	"onepark/gateway/internal/config"
	"onepark/gateway/internal/proxy"
)

// TestSplitHostPort 校验 Nacos 地址解析: 缺省端口回退 8848, 带端口正确拆分.
func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		addr     string
		wantHost string
		wantPort uint64
	}{
		{"nacos:8848", "nacos", 8848},
		{"127.0.0.1:8848", "127.0.0.1", 8848},
		{"nacos", "nacos", 8848}, // 缺省端口回退
	}
	for _, c := range cases {
		h, p := splitHostPort(c.addr)
		if h != c.wantHost || p != c.wantPort {
			t.Fatalf("splitHostPort(%q)=(%q,%d) want (%q,%d)", c.addr, h, p, c.wantHost, c.wantPort)
		}
	}
}

// TestApplyUpstreams 固化"动态上游热更新"解析路径:
//   - 合法 JSON 应成功替换路由表(不报错);
//   - 非法 JSON 应返回错误(调用方保留旧路由, 不崩溃 -> Nacos 不可达/格式错误时优雅降级).
func TestApplyUpstreams(t *testing.T) {
	gw, err := proxy.NewGateway(config.Config{Upstreams: []config.UpstreamConf{
		{Prefix: "/api/x", Target: "http://127.0.0.1:1"},
	}})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// 合法配置: 成功替换.
	good := `[{"prefix":"/api/y","target":"http://127.0.0.1:2"}]`
	if err := applyUpstreams(gw, good); err != nil {
		t.Fatalf("合法上游表应解析成功: %v", err)
	}

	// 非法配置: 返回错误(不 panic, 旧路由保留).
	if err := applyUpstreams(gw, "not-json"); err == nil {
		t.Fatal("非法上游表应返回错误")
	}
}
