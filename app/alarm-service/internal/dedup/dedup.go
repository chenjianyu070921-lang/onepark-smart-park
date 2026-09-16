// Package dedup 提供告警消费端的消息级幂等去重(L1).
// 约定: 去重组件不可用必须返回错误, 由消费端按可重试错误处理, 不降级放行(宁积压不误报).
// L2 业务冷却、L3 MySQL uk_request_id 唯一索引另见 docs/m3/06 §4.
package dedup

import (
	"context"
	"time"

	"onepark/common/redisx"
)

// Deduper 幂等去重器: 同一 key 首次调用返回 false, 重复调用返回 true.
type Deduper interface {
	// Seen 判定 key 是否已处理过. Redis 不可用时返回 error, 调用方不得跳过去重.
	Seen(ctx context.Context, key string) (bool, error)
}

// redisDeduper 基于 go-redis SetNX 的实现, TTL 决定幂等窗口(默认 24h).
type redisDeduper struct {
	rdb *redisx.Client
	ttl time.Duration
}

// NewRedisDeduper 构造基于 Redis 的幂等去重器.
func NewRedisDeduper(rdb *redisx.Client, ttl time.Duration) Deduper {
	return &redisDeduper{rdb: rdb, ttl: ttl}
}

func (d *redisDeduper) Seen(ctx context.Context, key string) (bool, error) {
	// SetNX: 写入成功(false)表示该消息首次处理; 写入失败(true)表示已处理过.
	ok, err := d.rdb.SetNX(ctx, key, "1", d.ttl).Result()
	if err != nil {
		return false, err
	}
	return !ok, nil
}
