package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"onepark/common/jwt"
)

// downstream 回显网关注入的 x-tenant-id, 用于断言 Auth 中间件正确透传身份.
func downstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Downstream-Tenant", r.Header.Get("x-tenant-id"))
		w.WriteHeader(http.StatusOK)
	}
}

func TestAuth(t *testing.T) {
	const secret = "gw-secret"
	auth := Auth(secret, map[string]bool{})(downstream())

	// 1) 无 token -> 401
	rec := httptest.NewRecorder()
	auth.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workorders", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}

	// 2) 伪造 token -> 401
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req2.Header.Set("Authorization", "Bearer garbage")
	auth.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", rec2.Code)
	}

	// 3) 有效 token -> 200 且下游收到 x-tenant-id
	tok, _ := jwt.Generate(secret, 100, "1,2", 5, jwt.TypeAccess, 7200)
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req3.Header.Set("Authorization", "Bearer "+tok)
	auth.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", rec3.Code)
	}
	if rec3.Header().Get("X-Downstream-Tenant") != "5" {
		t.Fatalf("downstream tenant = %q, want 5", rec3.Header().Get("X-Downstream-Tenant"))
	}

	// 4) 白名单路径放行(无需 token)
	authSkip := Auth(secret, map[string]bool{"/api/auth/login": true})(downstream())
	rec4 := httptest.NewRecorder()
	authSkip.ServeHTTP(rec4, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil))
	if rec4.Code != http.StatusOK {
		t.Fatalf("skip path: want 200, got %d", rec4.Code)
	}
}
