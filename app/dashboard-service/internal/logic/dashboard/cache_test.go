package dashboard

import (
	"context"
	"testing"
	"time"

	"onepark/app/dashboard-service/internal/svc"
)

// 本文件验证「事件驱动的缓存失效」—— 见 cache.go。
//
// 复用 overview_test.go 里已有的 openTestRedis(连不上则跳过), 不再重复定义。

// TestInvalidateOverviewCache 失效必须"精准": 清掉概览缓存, 但不能误伤其它 key.
func TestInvalidateOverviewCache(t *testing.T) {
	rdb := openTestRedis(t)
	ctx := context.Background()
	svcCtx := &svc.ServiceContext{Redis: rdb}

	// 用纳秒级唯一租户, 避免与其它测试/线上缓存冲突
	tenantA := time.Now().UnixNano()
	tenantB := tenantA + 1
	bystander := "m5:dashboard:other:bystander"

	for _, kv := range []struct {
		key string
		val string
	}{
		{overviewCacheKey(tenantA), `{"tenant":"A"}`},
		{overviewCacheKey(tenantB), `{"tenant":"B"}`},
		{bystander, "keep-me"},
	} {
		if err := rdb.Set(ctx, kv.key, []byte(kv.val), time.Minute).Err(); err != nil {
			t.Fatalf("准备缓存失败: %v", err)
		}
	}
	t.Cleanup(func() {
		rdb.Del(context.Background(), overviewCacheKey(tenantA), overviewCacheKey(tenantB), bystander)
	})

	n, err := InvalidateOverviewCache(ctx, svcCtx)
	if err != nil {
		t.Fatalf("失效失败: %v", err)
	}
	if n < 2 {
		t.Errorf("删除 key 数 = %d, 期望 >= 2(两个租户都要失效)", n)
	}

	// 概览缓存必须已被清掉
	for name, key := range map[string]string{"A": overviewCacheKey(tenantA), "B": overviewCacheKey(tenantB)} {
		if _, err := rdb.Get(ctx, key).Result(); err == nil {
			t.Errorf("租户 %s 的概览缓存未被清除", name)
		}
	}

	// 旁路 key 必须还在 —— 失效范围一旦放大到全库就是事故
	if got, err := rdb.Get(ctx, bystander).Result(); err != nil || got != "keep-me" {
		t.Errorf("旁路 key 被误删: val=%q err=%v", got, err)
	}
}

// TestInvalidateOverviewCache_NoRedis 未接 Redis 时应静默返回, 不 panic.
func TestInvalidateOverviewCache_NoRedis(t *testing.T) {
	n, err := InvalidateOverviewCache(context.Background(), &svc.ServiceContext{})
	if err != nil {
		t.Errorf("未接 Redis 不应报错: %v", err)
	}
	if n != 0 {
		t.Errorf("未接 Redis 应返回 0, 实际 %d", n)
	}

	// nil svcCtx 也不能 panic
	if _, err := InvalidateOverviewCache(context.Background(), nil); err != nil {
		t.Errorf("nil ServiceContext 不应报错: %v", err)
	}
}
