package logic

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/jwt"
	"onepark/common/redisx"
	"onepark/common/tokenblk"
)

// mustRedis 起一个进程内 miniredis, 使令牌黑名单/refresh 登记表相关用例
// 不再依赖外部 Redis(旧实现依赖 127.0.0.1:6379, 不可达即 Skip -> CI 不可见).
// 改用 miniredis 后这些用例必跑, 真正固化"注销失效 / 刷新轮换"语义, 减少漏测.
func mustRedis(t *testing.T) *redisx.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis 启动失败: %v", err)
	}
	t.Cleanup(mr.Close)
	return redisx.NewClient(&redisx.RedisConf{Addr: mr.Addr()})
}

func newTestCtxWithRedis(t *testing.T, rdb *redisx.Client) *svc.ServiceContext {
	t.Helper()
	return &svc.ServiceContext{JwtSecret: testSecret, JwtExpire: 7200, JwtRefresh: 86400, Redis: rdb}
}

// TestVerifyLogicRespectsBlacklist 固化"吊销(注销)语义收口到 Verify"的闭环:
// 未吊销 -> Verify 有效; 将 jti 写入 Redis 黑名单后 -> Verify 失效.
// 基于 miniredis, 必跑(替换原依赖外部 Redis 的 Skip 版).
func TestVerifyLogicRespectsBlacklist(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	tok := mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600)

	// 1) 未吊销: 应有效.
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: tok}); err != nil || !resp.Valid {
		t.Fatalf("未吊销应有效: err=%v resp=%+v", err, resp)
	}

	// 2) 注销(吊销 jti), TTL=令牌剩余有效期.
	claims, err := jwt.Parse(testSecret, tok)
	if err != nil {
		t.Fatalf("解析 token 失败: %v", err)
	}
	if err := tokenblk.Revoke(context.Background(), rdb, claims.ID, 3600*time.Second); err != nil {
		t.Fatalf("吊销 token 失败: %v", err)
	}

	// 3) 已吊销: 应失效.
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: tok}); err != nil || resp.Valid {
		t.Fatalf("已吊销应失效: err=%v resp=%+v", err, resp)
	}
}

// TestLogoutLogicRevokesRefresh 校验注销 refresh 令牌会将其从登记表吊销,
// 使其立即失效(真正结束会话, 而非仅黑 access).
func TestLogoutLogicRevokesRefresh(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	rt := mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 86400)

	// 模拟登录时已登记 refresh jti.
	rc, err := jwt.Parse(testSecret, rt)
	if err != nil {
		t.Fatalf("解析 refresh 失败: %v", err)
	}
	if err := tokenblk.StoreRefresh(context.Background(), rdb, rc.ID, 86400*time.Second); err != nil {
		t.Fatalf("登记 refresh 失败: %v", err)
	}
	if !tokenblk.RefreshExists(context.Background(), rdb, rc.ID) {
		t.Fatal("登记后 refresh 应存在")
	}

	if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: rt}); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if tokenblk.RefreshExists(context.Background(), rdb, rc.ID) {
		t.Fatal("注销后 refresh 应被吊销")
	}
}

// TestRefreshLogicRejectsUnknownRefresh 校验未在 Redis 登记的 refresh 令牌被拒,
// 防止离线的 refresh 令牌被重用(与登录时 StoreRefresh 配套).
func TestRefreshLogicRejectsUnknownRefresh(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	// 直接签发 refresh 但不登记 -> 刷新应被拒.
	rt := mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 86400)

	_, err := NewRefreshLogic(context.Background(), ctx).Refresh(&types.RefreshReq{RefreshToken: rt})
	if err == nil {
		t.Fatal("未登记的 refresh 应被拒绝")
	}
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrUnauthorized {
		t.Fatalf("期望 ErrUnauthorized, 实际 %v", err)
	}
}

// 说明: 刷新令牌轮换(旧 jti 吊销 + 新 jti 登记)位于 Refresh 的"DB 查询账号状态"之后,
// 单测无法注入 sys_db, 故轮换语义交由集成测试(TestLoginLogicIntegration 同类路径)与联调覆盖;
// 本文件仅固化"refresh 登记表"的单元行为(见 TestLogoutLogicRevokesRefresh / TestRefreshLogicRejectsUnknownRefresh).

// TestLogoutAccessRevokesPairedRefresh 固化"用 access 令牌注销会连带吊销配套 refresh"的闭环:
// 否则 refresh 白名单残留, 攻击者仍可用 refresh 换发新 access, 注销未真正结束会话.
func TestLogoutAccessRevokesPairedRefresh(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	at := mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600)
	rt := mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 86400)

	ac, err := jwt.Parse(testSecret, at)
	if err != nil {
		t.Fatalf("解析 access 失败: %v", err)
	}
	rc, err := jwt.Parse(testSecret, rt)
	if err != nil {
		t.Fatalf("解析 refresh 失败: %v", err)
	}
	// 模拟登录时已登记 refresh 白名单 + access↔refresh 配对.
	if err := tokenblk.StoreRefresh(context.Background(), rdb, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := tokenblk.LinkPair(context.Background(), rdb, ac.ID, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}
	if !tokenblk.RefreshExists(context.Background(), rdb, rc.ID) {
		t.Fatal("登记后 refresh 应存在")
	}

	// 用 access 令牌注销.
	if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: at}); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	// access 应入黑名单.
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: at}); err != nil || resp.Valid {
		t.Fatal("注销后 access 应失效")
	}
	// 配套 refresh 应被连带吊销.
	if tokenblk.RefreshExists(context.Background(), rdb, rc.ID) {
		t.Fatal("注销 access 应连带吊销配套 refresh")
	}
}

// TestLogoutAccessThenVerifyInvalid 端到端固化"tokenblk <-> auth 登出"集成闭环:
// 走 Logout 逻辑吊销 access 令牌(内部调用 tokenblk.Revoke)后, 同一令牌经 Verify 逻辑应判定失效.
// 这是对 TestVerifyLogicRespectsBlacklist(直接调用 tokenblk.Revoke)的补充 —— 真实走完 Logout 入口,
// 覆盖"用户注销 -> 网关/服务端后续校验立即失效"这一 P0 语义.
func TestLogoutAccessThenVerifyInvalid(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	tok := mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600)

	// 注销前 Verify 应有效.
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: tok}); err != nil || !resp.Valid {
		t.Fatalf("注销前应有效: err=%v resp=%+v", err, resp)
	}

	// 走 Logout 逻辑 -> tokenblk.Revoke(access jti, TTL=令牌剩余有效期).
	if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: tok}); err != nil {
		t.Fatalf("注销失败: %v", err)
	}

	// 注销后经 Verify 应失效(完整集成闭环).
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: tok}); err != nil || resp.Valid {
		t.Fatalf("注销后应失效: err=%v resp=%+v", err, resp)
	}
}

// TestLogoutAccessBlocksPairedRefreshRoundTrip 端到端固化 tokenblk<->auth 登出集成闭环:
// 模拟登录(登记 refresh 白名单 + access↔refresh 配对) -> 用 access 注销(连带吊销配套 refresh)
// -> 再用该 refresh 去刷新, 必须在 Refresh 逻辑中于 DB 查询之前被拒(refresh.go:39 的 RefreshExists 失败).
// 这比 TestLogoutAccessRevokesPairedRefresh 更强: 真实走完 Refresh 入口, 证明"注销真正结束会话",
// 攻击者拿到配套 refresh 也换不出新 access(而非仅单测 tokenblk.RefreshExists 返回 false).
func TestLogoutAccessBlocksPairedRefreshRoundTrip(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	at := mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600)
	rt := mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 86400)

	// 模拟登录: 登记 refresh 白名单 + access↔refresh 配对.
	ac, err := jwt.Parse(testSecret, at)
	if err != nil {
		t.Fatalf("解析 access 失败: %v", err)
	}
	rc, err := jwt.Parse(testSecret, rt)
	if err != nil {
		t.Fatalf("解析 refresh 失败: %v", err)
	}
	if err := tokenblk.StoreRefresh(context.Background(), rdb, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := tokenblk.LinkPair(context.Background(), rdb, ac.ID, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}

	// 用 access 令牌注销 -> 连带吊销配套 refresh(tokenblk).
	if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: at}); err != nil {
		t.Fatalf("注销失败: %v", err)
	}

	// 用被连带吊销的 refresh 去刷新: 必须在 DB 查询前被拒(ErrUnauthorized).
	_, err = NewRefreshLogic(context.Background(), ctx).Refresh(&types.RefreshReq{RefreshToken: rt})
	if err == nil {
		t.Fatal("配套 refresh 被注销后仍应刷新被拒")
	}
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrUnauthorized {
		t.Fatalf("期望 ErrUnauthorized, 实际 %v", err)
	}
}

// TestLogoutRefreshRevokesPairedAccess 固化"用 refresh 令牌注销也连带吊销配套 access"(双向联动):
// 模拟登录(登记 refresh 白名单 + 双向配对) -> 用 refresh 注销 -> 配套 access 应被拉黑,
// 否则会话未彻底结束(access 仍可用于鉴权直到自然过期).
func TestLogoutRefreshRevokesPairedAccess(t *testing.T) {
	rdb := mustRedis(t)
	ctx := newTestCtxWithRedis(t, rdb)
	at := mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600)
	rt := mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 86400)

	ac, err := jwt.Parse(testSecret, at)
	if err != nil {
		t.Fatalf("解析 access 失败: %v", err)
	}
	rc, err := jwt.Parse(testSecret, rt)
	if err != nil {
		t.Fatalf("解析 refresh 失败: %v", err)
	}
	if err := tokenblk.StoreRefresh(context.Background(), rdb, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := tokenblk.LinkPair(context.Background(), rdb, ac.ID, rc.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := tokenblk.LinkRefreshAccess(context.Background(), rdb, rc.ID, ac.ID, 86400*time.Second); err != nil {
		t.Fatal(err)
	}

	// 用 refresh 令牌注销.
	if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: rt}); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	// refresh 应被吊销.
	if tokenblk.RefreshExists(context.Background(), rdb, rc.ID) {
		t.Fatal("注销后 refresh 应被吊销")
	}
	// 配套 access 也应被拉黑(Verify 失效).
	if resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: at}); err != nil || resp.Valid {
		t.Fatal("注销 refresh 应连带使配套 access 失效")
	}
}

