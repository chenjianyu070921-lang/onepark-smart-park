// Package dedup 告警消费幂等去重(L1).
//
// 实现已上移到 onepark/common/dedup(parking / visitor / access 同样需要),
// 本包保留同名类型与构造函数作为薄封装: 告警主链路的调用点与既有用例无需改动,
// 同时保证"Redis 不可用即报错、不降级放行"这套语义全仓只有一份实现.
package dedup

import (
	"time"

	"onepark/common/dedup"
	"onepark/common/redisx"
)

// Deduper 幂等去重器, 语义见 onepark/common/dedup.
type Deduper = dedup.Deduper

// NewRedisDeduper 构造基于 Redis 的幂等去重器.
func NewRedisDeduper(rdb *redisx.Client, ttl time.Duration) Deduper {
	return dedup.NewRedisDeduper(rdb, ttl)
}
