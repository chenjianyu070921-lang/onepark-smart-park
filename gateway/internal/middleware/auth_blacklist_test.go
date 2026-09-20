package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onepark/common/ctxdata"
	"onepark/common/jwt"
	"onepark/common/redisx"
	"onepark/common/tokenblk"
	commonmw "onepark/common/middleware"
)

// TestTokenInvalidationClosedLoop 实证"Token 失效机制"闭环(代码级, 复用真实生产中间件 + 真实 Redis):
//
//	登录签发 JWT(含 jti) → 网关 Auth 放行 → logout 将 jti 写入 Redis 黑名单 → 同一 token 再次过网关被 401 拒绝.
//
// 这是"注销即失效"在网关唯一验签点的运行时证明: 不走 mock, 直接打真实 redisx.Client 与 middleware.Auth.
// 若本地 Redis(127.0.0.1:6379) 不可达则 Skip(不影响 CI), 实际联调环境会真实执行.
func TestTokenInvalidationClosedLoop(t *testing.T) {
	const secret = "gw-secret"

	rdb := redisx.NewClient(&redisx.RedisConf{Addr: "127.0.0.1:6379"})
	defer rdb.Close()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("跳过: 本地 Redis(127.0.0.1:6379) 不可达: %v", err)
	}

	// 下游桩: 记录是否被放行(next 调用) 与网关注入的身份.
	var passed bool
	var gotUser int64
	downstream := func(w http.ResponseWriter, r *http.Request) {
		passed = true
		gotUser = ctxdata.GetUserId(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	// rdb 传真实客户端: 注销写入的黑名单一经网关读取即生效.
	chain := Auth(secret, rdb)(commonmw.IdentityFromHeader(downstream))

	// 登录签发 access token: user=100, tenant=5, roles=1,2
	tok, err := jwt.Generate(secret, 100, "1,2", 5, jwt.TypeAccess, 7200)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	// 1) 未吊销: 网关应放行, 且下游取到注入身份.
	passed = false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	chain.ServeHTTP(rec, req)
	if !passed {
		t.Fatalf("未吊销 token 应放行, 但被拒: code=%d", rec.Code)
	}
	if gotUser != 100 {
		t.Errorf("下游 ctxdata userId = %d, 期望 100", gotUser)
	}

	// 2) 注销(吊销 jti): 与 logout 逻辑一致, 将 jti 写入黑名单, TTL=令牌剩余有效期.
	claims, err := jwt.Parse(secret, tok)
	if err != nil {
		t.Fatalf("解析 token 失败: %v", err)
	}
	if err := tokenblk.Revoke(context.Background(), rdb, claims.ID, 7200*time.Second); err != nil {
		t.Fatalf("吊销 token 失败: %v", err)
	}

	// 3) 已吊销: 网关应拒绝(next 不被调用, 返回 401).
	passed = false
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/workorders", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	chain.ServeHTTP(rec2, req2)
	if passed {
		t.Fatalf("已吊销 token 不应放行")
	}
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("已吊销 token 应 401, 实际 %d", rec2.Code)
	}
}
