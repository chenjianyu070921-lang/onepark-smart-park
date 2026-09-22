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

