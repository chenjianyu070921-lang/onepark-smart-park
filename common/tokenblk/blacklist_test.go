package tokenblk

import (
	"context"
	"testing"
	"time"
)

// TestDegradeWithoutRedis 验证 Redis 不可用时(传入 nil)降级放行: Revoke 无操作、IsRevoked 恒 false,
// 保证未配置 Redis 的环境注销/校验不阻断业务. 该路径无需真实 Redis, 可在任意环境跑通.
func TestDegradeWithoutRedis(t *testing.T) {
	ctx := context.Background()
	if err := Revoke(ctx, nil, "jti-1", time.Minute); err != nil {
		t.Fatalf("Revoke(nil) 应无错误, got=%v", err)
	}
	if IsRevoked(ctx, nil, "jti-1") {
		t.Error("IsRevoked(nil) 应恒返回 false")
	}
	// jti 为空也应安全降级
	if err := Revoke(ctx, nil, "", time.Minute); err != nil {
		t.Fatalf("Revoke(empty jti) 应无错误, got=%v", err)
	}
}
