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
