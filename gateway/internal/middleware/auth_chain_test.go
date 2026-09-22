package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	authpb "onepark/proto/auth"
	commonpb "onepark/proto/common"
	"onepark/common/ctxdata"
	commonmw "onepark/common/middleware"
	"google.golang.org/grpc"
)

// fakeAuthClient 实现 authpb.AuthServiceClient 的内存假实现, 用于验证网关"委托 gRPC Verify"
// 这一关键链路, 不依赖真实 auth-service / Redis.
type fakeAuthClient struct {
	valid  bool
	userID int64
	roles  string
	tenant int64
}

func (f *fakeAuthClient) Verify(_ context.Context, _ *authpb.VerifyReq, _ ...grpc.CallOption) (*authpb.VerifyResp, error) {
	if !f.valid {
		return &authpb.VerifyResp{Valid: false}, nil
	}
	return &authpb.VerifyResp{Valid: true, UserId: f.userID, RoleIds: f.roles, TenantId: f.tenant}, nil
}
func (f *fakeAuthClient) Ping(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}
func (f *fakeAuthClient) Login(_ context.Context, _ *authpb.LoginReq, _ ...grpc.CallOption) (*authpb.LoginResp, error) {
	return &authpb.LoginResp{}, nil
}
func (f *fakeAuthClient) Refresh(_ context.Context, _ *authpb.RefreshReq, _ ...grpc.CallOption) (*authpb.LoginResp, error) {
	return &authpb.LoginResp{}, nil
}

// TestAuthDelegatesToGrpcAndInjectsIdentity 网关鉴权联调(代码级, 复用真实生产中间件):
//
//	提取 Token → 委托 auth gRPC Verify(假客户端返回有效身份) → 注入身份 Header → 下游 IdentityFromHeader 提升进 ctxdata.
//
// 断言"带 token 访问下游后, 下游 ctxdata 能取到 tenant/user/roles", 即网关 → 下游 ctxdata 跨服务边界贯通.
func TestAuthDelegatesToGrpcAndInjectsIdentity(t *testing.T) {
	var gotUser, gotTenant int64
	var gotRoles string
	downstreamFn := func(w http.ResponseWriter, r *http.Request) {
		gotUser = ctxdata.GetUserId(r.Context())
		gotTenant = ctxdata.GetTenantId(r.Context())
		gotRoles = ctxdata.GetRoleIds(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	chain := Auth(&fakeAuthClient{valid: true, userID: 100, roles: "1,2", tenant: 5})(
		commonmw.IdentityFromHeader(downstreamFn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
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

// TestAuthRejectsInvalidTokenViaGrpc 无效令牌(Verify 返回 valid=false)必须 401, 体现失败闭环.
func TestAuthRejectsInvalidTokenViaGrpc(t *testing.T) {
	downstream := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	chain := Auth(&fakeAuthClient{valid: false})(commonmw.IdentityFromHeader(downstream))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req.Header.Set("Authorization", "Bearer bad")
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效 token 应 401, 实际 %d", rec.Code)
	}
}

// TestAuthPublicPathsSkipVerify 鉴权引导端点(login/refresh/verify/logout)不应调用 gRPC, 直接放行.
func TestAuthPublicPathsSkipVerify(t *testing.T) {
	downstream := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	chain := Auth(&fakeAuthClient{valid: false})(commonmw.IdentityFromHeader(downstream))
	for _, p := range []string{"/api/auth/login", "/api/auth/refresh", "/api/auth/verify", "/api/auth/logout"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, p, nil)
		chain.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应公开放行, 实际 %d", p, rec.Code)
		}
	}
}

// TestAuthMissingToken 缺 Token 一律 401(不调用 gRPC).
func TestAuthMissingToken(t *testing.T) {
	downstream := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	chain := Auth(&fakeAuthClient{valid: true})(commonmw.IdentityFromHeader(downstream))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺 token 应 401, 实际 %d", rec.Code)
	}
}
