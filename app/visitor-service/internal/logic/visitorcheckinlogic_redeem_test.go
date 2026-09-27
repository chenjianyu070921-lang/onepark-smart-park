package logic

import (
	"testing"
	"time"
)

// TestRedeemLockKey 验证防重放锁 key 按记录ID维度隔离, 与 DB CAS 的 tenant_id 隔离互补.
func TestRedeemLockKey(t *testing.T) {
	if got := redeemLockKey(123); got != "visitor:redeem:123" {
		t.Errorf("redeemLockKey(123) = %q, want %q", got, "visitor:redeem:123")
	}
}

// TestRedeemTTL 验证核销锁有效期(纯函数, 不依赖 Redis):
// 无过期/已过期兜底 24h; 过期时间在未来则返回到过期的正值.
func TestRedeemTTL(t *testing.T) {
	const fallback = 24 * time.Hour

	// 无过期 -> 兜底 24h
	if got := redeemTTL(nil); got != fallback {
		t.Errorf("redeemTTL(nil) = %v, want %v", got, fallback)
	}

	// 过期时间在未来 -> 返回正值, 约等于到过期的时间差(容差 1min 抵消调用耗时)
	future := time.Now().Add(2 * time.Hour)
	if got := redeemTTL(&future); got <= 0 || got > 2*time.Hour+time.Minute {
		t.Errorf("redeemTTL(future) = %v, 期望约 2h 正值", got)
	}

	// 过期时间已过去 -> 兜底 24h(不应返回负值导致锁立即失效)
	past := time.Now().Add(-time.Hour)
	if got := redeemTTL(&past); got != fallback {
		t.Errorf("redeemTTL(past) = %v, want %v", got, fallback)
	}
}
