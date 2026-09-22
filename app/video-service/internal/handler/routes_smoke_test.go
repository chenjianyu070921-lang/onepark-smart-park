package handler

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"onepark/app/video-service/internal/svc"

	"github.com/zeromicro/go-zero/rest"
)

// TestRoutes_RecordPlanAndPlaybackRoutable 真实拉起服务, 验证录像计划/回放路由与既有路由共存且可达.
//
// 必须真正 Start 的原因(与 alarm-service 的路由冒烟同一结论): Server.AddRoutes 只把路由
// 缓存进 engine, 真正的 router 绑定与 go-zero 的注册冲突检测发生在 Start() ——
// 不启动就发现不了 /api/video/record-plan/:id 与既有 /api/video/camera/:id 在路由树上的冲突.
//
// 判定刻意不看 200: 本地没有网关注入 x-tenant-id, handler 会因缺租户返回 400。
// 400 说明请求已命中 handler; 404 才是"路由压根没注册上"。
func TestRoutes_RecordPlanAndPlaybackRoutable(t *testing.T) {
	port := freePort(t)
	server := rest.MustNewServer(rest.RestConf{Host: "127.0.0.1", Port: port})
	defer server.Stop()

	RegisterHandlers(server, &svc.ServiceContext{})
	go server.Start()
	waitListening(t, port)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/video/record-plans"},
		{http.MethodGet, "/api/video/record-plan/1"},
		{http.MethodPost, "/api/video/record-plan"},
		{http.MethodPut, "/api/video/record-plan/1"},
		{http.MethodDelete, "/api/video/record-plan/1"},
		{http.MethodGet, "/api/video/playback?camera_id=1&start_time=1&end_time=2"},
		// 既有路由一并验证, 防止新路由抢占了老路径.
		{http.MethodGet, "/api/video/camera/1"},
		{http.MethodGet, "/api/video/stream/1"},
	}
	for _, c := range cases {
		code := statusOf(t, port, c.method, c.path)
		if code == http.StatusNotFound {
			t.Errorf("%s %s 未注册到路由(404), 检查 routes.go", c.method, c.path)
			continue
		}
		if code != http.StatusBadRequest {
			t.Errorf("%s %s 期望 400(缺租户/参数), 实际 %d", c.method, c.path, code)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 100; i++ {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("服务未在预期时间内监听 %s", addr)
}

func statusOf(t *testing.T, port int, method, path string) int {
	t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
