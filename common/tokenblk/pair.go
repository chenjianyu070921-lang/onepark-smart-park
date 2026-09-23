package tokenblk

import (
	"context"
	"time"

	"onepark/common/redisx"
)

const pairPrefix = "auth:pair:"

// LinkPair 记录 access jti 与其配套 refresh jti 的映射, TTL 取 refresh 有效期,
// 供"用 access 令牌注销"时连带吊销 refresh(否则 7d refresh 仍可换发新 access, 注销不生效).
// rdb 为空或任一 jti 为空时直接返回(降级: 不连带吊销, 与黑名单/refresh 登记表一致).
func LinkPair(ctx context.Context, rdb *redisx.Client, accessJti, refreshJti string, ttl time.Duration) error {
	if rdb == nil || accessJti == "" || refreshJti == "" {
		return nil
	}
	return rdb.Set(ctx, pairPrefix+accessJti, refreshJti, ttl).Err()
}

// PairedRefresh 取 access jti 配套的 refresh jti; 无记录或查询失败时返回空串(降级: 不连带吊销).
func PairedRefresh(ctx context.Context, rdb *redisx.Client, accessJti string) string {
	if rdb == nil || accessJti == "" {
		return ""
	}
	v, err := rdb.Get(ctx, pairPrefix+accessJti).Result()
	if err != nil {
		return ""
	}
	return v
}

// UnlinkPair 删除 access jti 的配对记录(注销后清理残留). rdb 为空或 jti 为空时直接返回.
func UnlinkPair(ctx context.Context, rdb *redisx.Client, accessJti string) error {
	if rdb == nil || accessJti == "" {
		return nil
	}
	return rdb.Del(ctx, pairPrefix+accessJti).Err()
}
