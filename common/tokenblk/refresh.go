package tokenblk

import (
	"context"
	"time"

	"onepark/common/redisx"
)

const refreshPrefix = "auth:refresh:"

// StoreRefresh 登记 refresh 令牌 jti, TTL 为其有效期(通常 7d).
// 用于刷新时校验令牌是否仍有效(未被注销/轮换吊销)以及审计归属.
// rdb 为空或 jti 为空时直接返回(降级: 无 Redis 则 refresh 退化为无状态, 不阻断登录/刷新).
func StoreRefresh(ctx context.Context, rdb *redisx.Client, jti string, ttl time.Duration) error {
	if rdb == nil || jti == "" {
		return nil
	}
	return rdb.Set(ctx, refreshPrefix+jti, "1", ttl).Err()
}

// RefreshExists 判断 refresh jti 是否已登记(未被吊销且未过期).
// rdb 为空或查询失败时返回 true(降级放行, 与黑名单一致: Redis 不可达不阻断刷新).
func RefreshExists(ctx context.Context, rdb *redisx.Client, jti string) bool {
	if rdb == nil || jti == "" {
		return true
	}
	n, err := rdb.Exists(ctx, refreshPrefix+jti).Result()
	if err != nil {
		return true
	}
	return n > 0
}

// RevokeRefresh 吊销 refresh jti(注销/刷新轮换时调用, 使其立即失效). rdb 为空或 jti 为空时直接返回.
func RevokeRefresh(ctx context.Context, rdb *redisx.Client, jti string) error {
	if rdb == nil || jti == "" {
		return nil
	}
	return rdb.Del(ctx, refreshPrefix+jti).Err()
}
