package logic

// unreadcache_test.go — 未读计数缓存纯函数与 nil 防御路径单测.
// 不依赖真实 Redis: 键格式与隔离性用表驱动断言; nil 客户端守卫验证"缓存不可用
// 静默降级不 panic"(对应线上未配置 Redis 的降级部署形态).

import (
	"context"
	"testing"
)

// TestUnreadCacheKey 验证缓存键格式与 (租户, 用户) 隔离性:
// 不同租户/用户必须生成不同键, 防止跨租户串数; 同键幂等可反复失效.
func TestUnreadCacheKey(t *testing.T) {
	cases := []struct {
		tenantID int64
		uid      int64
		want     string
	}{
		{1, 100, "notice:unread:cnt:1:100"},
		{2, 100, "notice:unread:cnt:2:100"}, // 同用户不同租户 → 不同键
		{1, 200, "notice:unread:cnt:1:200"}, // 同租户不同用户 → 不同键
	}
	seen := make(map[string]int64, len(cases))
	for _, c := range cases {
		got := unreadCacheKey(c.tenantID, c.uid)
		if got != c.want {
			t.Fatalf("unreadCacheKey(%d,%d) = %q, want %q", c.tenantID, c.uid, got, c.want)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("键冲突: (%d,%d) 与 (%d) 共用 %q", c.tenantID, c.uid, prev, got)
		}
		seen[got] = c.uid
	}
}

// TestUnreadCacheNilClient 验证缓存层对 nil 客户端的防御:
// 读返回未命中、写/删静默跳过, 均不得 panic —— 保证未配置 Redis 时直查 DB 的降级路径可用.
func TestUnreadCacheNilClient(t *testing.T) {
	ctx := context.Background()

	if _, ok := getUnreadCache(ctx, nil, 1, 100); ok {
		t.Fatal("nil 客户端读缓存应返回未命中")
	}
	setUnreadCache(ctx, nil, 1, 100, 5)   // 不应 panic
	invalidateUnreadCache(ctx, nil, 1, 100) // 不应 panic
}
