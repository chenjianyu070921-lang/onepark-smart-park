package dedup

import (
	"context"
	"time"

	"onepark/common/redisx"
)

// Cooldown 业务冷却去重(L2, docs/m3/08 §1): 抑制设备抖动(门磁反复触发/传感器毛刺),
// 冷却窗口内同设备同事件不重复生成告警. 与 L1 消息幂等(Deduper)语义不同:
// L1 针对同一消息的重放, L2 针对不同消息描述的同一业务事件.
type Cooldown interface {
	// TryAcquire 尝试占用冷却窗口: true 表示冷却中(应抑制), false 表示首次(可生成告警).
	// Redis 不可用时返回 error, 调用方不得降级放行(宁积压不误报, docs/m3/08 §5).
	TryAcquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// redisCooldown 基于 go-redis SetNX 的冷却实现.
type redisCooldown struct {
	rdb *redisx.Client
}

// NewRedisCooldown 构造基于 Redis 的业务冷却器.
func NewRedisCooldown(rdb *redisx.Client) Cooldown {
	return &redisCooldown{rdb: rdb}
}

func (c *redisCooldown) TryAcquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := c.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, err
	}
	// SetNX 失败(键已存在) = 冷却中.
	return !ok, nil
}
