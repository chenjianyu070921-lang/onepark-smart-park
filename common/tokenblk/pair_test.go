package tokenblk

import (
	"context"
	"testing"
	"time"
)

// TestLinkPairAndPairedRefresh 固化 access↔refresh 配对读写闭环.
func TestLinkPairAndPairedRefresh(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	if err := LinkPair(ctx, rdb, "a1", "r1", 7*24*time.Hour); err != nil {
		t.Fatalf("LinkPair: %v", err)
	}
	if got := PairedRefresh(ctx, rdb, "a1"); got != "r1" {
		t.Fatalf("PairedRefresh = %q, 期望 r1", got)
	}
	if got := PairedRefresh(ctx, rdb, "missing"); got != "" {
		t.Fatalf("PairedRefresh(missing) = %q, 期望空", got)
	}
	if err := UnlinkPair(ctx, rdb, "a1"); err != nil {
		t.Fatalf("UnlinkPair: %v", err)
	}
	if got := PairedRefresh(ctx, rdb, "a1"); got != "" {
		t.Fatalf("UnlinkPair 后应为空, 得 %q", got)
	}
}

// TestPairDegradeWithoutRedis 固化"Redis 未配置时降级"语义: 不 panic, 返回空.
func TestPairDegradeWithoutRedis(t *testing.T) {
	ctx := context.Background()
	if err := LinkPair(ctx, nil, "a", "r", time.Hour); err != nil {
		t.Fatalf("nil rdb LinkPair 应无错误, 得 %v", err)
	}
	if got := PairedRefresh(ctx, nil, "a"); got != "" {
		t.Fatalf("nil rdb PairedRefresh 应降级为空, 得 %q", got)
	}
	if err := UnlinkPair(ctx, nil, "a"); err != nil {
		t.Fatalf("nil rdb UnlinkPair 应无错误, 得 %v", err)
	}
}

// TestLinkRefreshAccessAndPairedAccess 固化 refresh→access 反向索引读写闭环(双向联动基础).
func TestLinkRefreshAccessAndPairedAccess(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	if err := LinkRefreshAccess(ctx, rdb, "r1", "a1", 7*24*time.Hour); err != nil {
		t.Fatalf("LinkRefreshAccess: %v", err)
	}
	if got := PairedAccess(ctx, rdb, "r1"); got != "a1" {
		t.Fatalf("PairedAccess = %q, 期望 a1", got)
	}
	if got := PairedAccess(ctx, rdb, "missing"); got != "" {
		t.Fatalf("PairedAccess(missing) = %q, 期望空", got)
	}
	if err := UnlinkRefreshPair(ctx, rdb, "r1"); err != nil {
		t.Fatalf("UnlinkRefreshPair: %v", err)
	}
	if got := PairedAccess(ctx, rdb, "r1"); got != "" {
		t.Fatalf("UnlinkRefreshPair 后应为空, 得 %q", got)
	}
}

// TestRevokePairedRefreshAtomic 固化"原子吊销配套 refresh"闭环: 一次调用清掉 refresh 登记表 + 双向配对,
// 模拟注销后 refresh 登记表与两方向配对记录均应消失(此前分别为两笔独立 Del, 非原子).
func TestRevokePairedRefreshAtomic(t *testing.T) {
	rdb, mini := newRDB(t)
	defer mini.Close()
	ctx := context.Background()

	if err := StoreRefresh(ctx, rdb, "r1", 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := LinkPair(ctx, rdb, "a1", "r1", 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := LinkRefreshAccess(ctx, rdb, "r1", "a1", 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	if err := RevokePairedRefresh(ctx, rdb, "a1", "r1"); err != nil {
		t.Fatalf("RevokePairedRefresh: %v", err)
	}
	if RefreshExists(ctx, rdb, "r1") {
		t.Fatal("原子吊销后 refresh 登记表应已删")
	}
	if got := PairedRefresh(ctx, rdb, "a1"); got != "" {
		t.Fatalf("原子吊销后 access→refresh 配对应已删, 得 %q", got)
	}
	if got := PairedAccess(ctx, rdb, "r1"); got != "" {
		t.Fatalf("原子吊销后 refresh→access 配对应已删, 得 %q", got)
	}
}
