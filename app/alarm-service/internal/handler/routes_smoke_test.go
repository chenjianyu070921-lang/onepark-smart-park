package handler

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/svc"

	"github.com/zeromicro/go-zero/rest"
)

// TestRoutes_AckResolveRoutable 真实拉起服务, 验证 #39/#40 与既有路由共存且可达.
//
// 必须真正 Start 的原因: Server.AddRoutes 只把路由缓存进 engine, 真正绑定 router
// (以及 go-zero 的注册冲突检测)发生在 Start() —— 不启动就无法发现 /api/alarm/:id/ack
// 与既有 /api/alarm/:id/status、/api/alarm/rule/:id 在路由树上的冲突.
// 这是本次新增路由唯一的风险点, 也是唯一能验证它的方式.
//
// 判定刻意不看 200: 本地没有网关注入 x-tenant-id, handler 会因缺租户返回 400(M3-W-1001).
// 400 说明请求已命中本文件的 handler; 404 才是"路由压根没注册上".
func TestRoutes_AckResolveRoutable(t *testing.T) {
	port := freePort(t)
	server := rest.MustNewServer(rest.RestConf{Host: "127.0.0.1", Port: port})
	defer server.Stop()

	RegisterHandlers(server, &svc.ServiceContext{Alarms: &statusActionStore{}})
	go server.Start()
	waitListening(t, port)

	// status 是改造前唯一的入口, 一并验证没有被新路由抢占.
	for _, path := range []string{"/api/alarm/321/ack", "/api/alarm/321/resolve", "/api/alarm/321/status"} {
		code := statusOfPut(t, port, path)
		if code == http.StatusNotFound {
			t.Errorf("%s 未注册到路由(404), 检查 routes.go", path)
			continue
		}
		if code != http.StatusBadRequest {
			t.Errorf("%s 期望 400(缺租户), 实际 %d", path, code)
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

func statusOfPut(t *testing.T, port int, path string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
