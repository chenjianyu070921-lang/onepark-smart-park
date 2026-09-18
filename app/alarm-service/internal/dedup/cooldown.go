package dedup

import (
	"onepark/common/dedup"
	"onepark/common/redisx"
)

// Cooldown 业务冷却去重(L2), 语义见 onepark/common/dedup.
type Cooldown = dedup.Cooldown

// NewRedisCooldown 构造基于 Redis 的业务冷却器.
func NewRedisCooldown(rdb *redisx.Client) Cooldown {
	return dedup.NewRedisCooldown(rdb)
}
