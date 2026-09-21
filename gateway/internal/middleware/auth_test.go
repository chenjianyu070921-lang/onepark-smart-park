package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// downstream 回显网关注入的 x-tenant-id, 用于断言 Auth 中间件正确透传身份.
func downstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Downstream-Tenant", r.Header.Get("x-tenant-id"))
		w.WriteHeader(http.StatusOK)
	}
}

// TestAuth 验证网关鉴权中间件(委托 auth gRPC Verify 后)的关键行为:
// 缺 token / gRPC 返回无效 / 公共引导端点 / 受保护接口缺 token.
func TestAuth(t *testing.T) {
	valid := &fakeAuthClient{valid: true, userID: 100, roles: "1,2", tenant: 5}
	invalid := &fakeAuthClient{valid: false}

	// 1) 无 token -> 401
	auth := Auth(valid)(downstream())
	rec := httptest.NewRecorder()
	auth.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workorders", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}

	// 2) 有效(委托 gRPC 返回 valid) -> 200 且下游收到 x-tenant-id
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req3.Header.Set("Authorization", "Bearer sometoken")
	auth.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", rec3.Code)
	}
	if rec3.Header().Get("X-Downstream-Tenant") != "5" {
		t.Fatalf("downstream tenant = %q, want 5", rec3.Header().Get("X-Downstream-Tenant"))
	}

	// 3) 无效(委托 gRPC 返回 invalid, 含伪冒/过期令牌) -> 401
	bad := Auth(invalid)(downstream())
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req2.Header.Set("Authorization", "Bearer garbage")
	bad.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", rec2.Code)
	}

	// 4) 鉴权引导端点公开(无需 token, 不调 gRPC)
	authPublic := Auth(invalid)(downstream())
	rec4 := httptest.NewRecorder()
	authPublic.ServeHTTP(rec4, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil))
	if rec4.Code != http.StatusOK {
		t.Fatalf("public auth path: want 200, got %d", rec4.Code)
	}

	// 5) 其它业务接口无 token -> 401(全接口强制校验)
	rec5 := httptest.NewRecorder()
	auth.ServeHTTP(rec5, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if rec5.Code != http.StatusUnauthorized {
		t.Fatalf("protected path without token: want 401, got %d", rec5.Code)
	}
}
