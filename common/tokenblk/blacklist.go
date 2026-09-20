// Package tokenblk 提供 JWT 注销黑名单(主动吊销)的 Redis 封装, 供 auth-service 写入、网关校验共享.
// 设计: 以 jti 为键写入 Redis, TTL 等于令牌剩余有效期; 校验时查键存在即视为已吊销.
// Redis 不可用时(未配置或查询失败)一律降级放行, 不阻断业务.
package tokenblk

import (
	"context"
	"time"

	"onepark/common/redisx"
)

const keyPrefix = "tokblk:"

// Revoke 将 jti 加入黑名单, ttl 为令牌剩余有效期. rdb 为空或 jti 为空时直接返回(无操作).
func Revoke(ctx context.Context, rdb *redisx.Client, jti string, ttl time.Duration) error {
	if rdb == nil || jti == "" {
		return nil
	}
	return rdb.Set(ctx, keyPrefix+jti, "1", ttl).Err()
}

// IsRevoked 判断 jti 是否已被吊销. rdb 为空或查询失败时返回 false(降级放行).
func IsRevoked(ctx context.Context, rdb *redisx.Client, jti string) bool {
	if rdb == nil || jti == "" {
		return false
	}
	n, err := rdb.Exists(ctx, keyPrefix+jti).Result()
	if err != nil {
		return false
	}
	return n > 0
}
