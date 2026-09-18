package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"onepark/common/ctxdata"
	"onepark/common/jwt"
	commonmw "onepark/common/middleware"
)

// TestJWTToDownstreamCtxdata 端到端鉴权联调(代码级, 复用真实生产中间件):
//
//	登录签发的 JWT → 网关 Auth 校验并注入身份 Header → 下游 IdentityFromHeader 提升进 ctxdata.
//
// 断言"带 token 访问下游后, 下游 ctxdata 能取到 tenant/user/roles", 即 M6 P0-2 的联调目标.
// 与 auth_test.go 的区别: 该用例把网关 Auth 与下游 IdentityFromHeader 串成一条真实链路,
// 而非仅断言网关透传的原始 Header, 从而覆盖"网关 → 下游 ctxdata"这一跨服务边界.
func TestJWTToDownstreamCtxdata(t *testing.T) {
	const secret = "gw-secret"

	var gotUser, gotTenant int64
	var gotRoles string
	downstreamFn := func(w http.ResponseWriter, r *http.Request) {
		gotUser = ctxdata.GetUserId(r.Context())
		gotTenant = ctxdata.GetTenantId(r.Context())
		gotRoles = ctxdata.GetRoleIds(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	// 真实链路: 网关 Auth → 下游 IdentityFromHeader.
	chain := Auth(secret)(commonmw.IdentityFromHeader(downstreamFn))

	// 登录签发 access token: user=100, tenant=5, roles=1,2
	tok, err := jwt.Generate(secret, 100, "1,2", 5, jwt.TypeAccess, 7200)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("链路返回码 = %d, 期望 200", rec.Code)
	}
	if gotUser != 100 {
		t.Errorf("下游 ctxdata userId = %d, 期望 100", gotUser)
	}
	if gotTenant != 5 {
		t.Errorf("下游 ctxdata tenantId = %d, 期望 5", gotTenant)
	}
	if gotRoles != "1,2" {
		t.Errorf("下游 ctxdata roleIds = %q, 期望 1,2", gotRoles)
	}
}
