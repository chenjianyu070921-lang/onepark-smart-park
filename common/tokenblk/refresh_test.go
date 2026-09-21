package tokenblk

import (
	"context"
	"testing"
	"time"

	"onepark/common/redisx"

	"github.com/alicebob/miniredis/v2"
)

func newRDB(t *testing.T) (*redisx.Client, *miniredis.Miniredis) {
	t.Helper()
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	return redisx.NewClient(&redisx.RedisConf{Addr: mini.Addr()}), mini
}

// TestRefreshStoreAndRevoke 验证 refresh 令牌登记/存在性/吊销闭环.
func TestRefreshStoreAndRevoke(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	if err := StoreRefresh(ctx, rdb, "jti-1", 7*24*time.Hour); err != nil {
		t.Fatalf("StoreRefresh: %v", err)
	}
	if !RefreshExists(ctx, rdb, "jti-1") {
		t.Fatal("登记后 RefreshExists 应为 true")
	}
	if err := RevokeRefresh(ctx, rdb, "jti-1"); err != nil {
		t.Fatalf("RevokeRefresh: %v", err)
	}
	if RefreshExists(ctx, rdb, "jti-1") {
		t.Fatal("吊销后 RefreshExists 应为 false")
	}
}

// TestRefreshRotation 验证刷新令牌轮换: 旧 jti 吊销, 新 jti 生效.
func TestRefreshRotation(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	_ = StoreRefresh(ctx, rdb, "old", 7*24*time.Hour)
	// 轮换: 吊销旧, 登记新
	_ = RevokeRefresh(ctx, rdb, "old")
	_ = StoreRefresh(ctx, rdb, "new", 7*24*time.Hour)

	if RefreshExists(ctx, rdb, "old") {
		t.Fatal("旧 refresh jti 轮换后应已吊销")
	}
	if !RefreshExists(ctx, rdb, "new") {
		t.Fatal("新 refresh jti 应已生效")
	}
}

// TestRefreshTTLExpiry 验证 refresh 登记表随 TTL 自然过期(7d 窗口).
func TestRefreshTTLExpiry(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	_ = StoreRefresh(ctx, rdb, "exp", time.Hour)
	if !RefreshExists(ctx, rdb, "exp") {
		t.Fatal("TTL 内应存在")
	}
	mini.FastForward(2 * time.Hour) // 跳过 TTL
	if RefreshExists(ctx, rdb, "exp") {
		t.Fatal("TTL 过期后应不存在")
	}
}

// TestRefreshDegradeWithoutRedis 固化"Redis 未配置时降级"语义: 不 panic, 校验放行.
func TestRefreshDegradeWithoutRedis(t *testing.T) {
	ctx := context.Background()
	if err := StoreRefresh(ctx, nil, "x", time.Hour); err != nil {
		t.Fatalf("nil rdb StoreRefresh 应无错误, 得 %v", err)
	}
	if !RefreshExists(ctx, nil, "x") {
		t.Fatal("nil rdb RefreshExists 应降级为 true(放行)")
	}
	if err := RevokeRefresh(ctx, nil, "x"); err != nil {
		t.Fatalf("nil rdb RevokeRefresh 应无错误, 得 %v", err)
	}
}
