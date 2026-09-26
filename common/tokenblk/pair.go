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

// rpairPrefix 反向索引: refresh jti -> 配套 access jti, 与 pairPrefix(access→refresh) 构成双向索引.
// 仅维护单向配对时, "用 refresh 令牌注销"只能吊销 refresh 本身, 配对 access 仍有效至自然过期,
// 会话未彻底结束; 双向索引使 refresh 注销也能连带吊销 access.
const rpairPrefix = "auth:rpair:"

// LinkRefreshAccess 记录 refresh jti 配套的 access jti(双向索引的另一方向), TTL 取 access 有效期.
// rdb 为空或任一 jti 为空时直接返回(降级: 不连带吊销, 与 LinkPair 一致).
func LinkRefreshAccess(ctx context.Context, rdb *redisx.Client, refreshJti, accessJti string, ttl time.Duration) error {
	if rdb == nil || refreshJti == "" || accessJti == "" {
		return nil
	}
	return rdb.Set(ctx, rpairPrefix+refreshJti, accessJti, ttl).Err()
}

// PairedAccess 取 refresh jti 配套的 access jti; 无记录或查询失败时返回空串(降级: 不连带吊销).
func PairedAccess(ctx context.Context, rdb *redisx.Client, refreshJti string) string {
	if rdb == nil || refreshJti == "" {
		return ""
	}
	v, err := rdb.Get(ctx, rpairPrefix+refreshJti).Result()
	if err != nil {
		return ""
	}
	return v
}

// UnlinkRefreshPair 删除 refresh→access 配对记录(注销后清理残留). rdb 为空或 jti 为空时直接返回.
func UnlinkRefreshPair(ctx context.Context, rdb *redisx.Client, refreshJti string) error {
	if rdb == nil || refreshJti == "" {
		return nil
	}
	return rdb.Del(ctx, rpairPrefix+refreshJti).Err()
}

// RevokePairedRefresh 原子吊销 access 配套的 refresh: 删除 refresh 登记表 + 双向配对记录(access→refresh 与 refresh→access),
// 用 TxPipeline 保证三步同步, 避免"仅完成其中一两步"导致配对残留(此前分别为两笔独立 Del 写, 非原子).
// 无 Redis 或任一 jti 为空时直接返回(降级: 不阻断注销).
func RevokePairedRefresh(ctx context.Context, rdb *redisx.Client, accessJti, refreshJti string) error {
	if rdb == nil || accessJti == "" || refreshJti == "" {
		return nil
	}
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, refreshPrefix+refreshJti)
	pipe.Del(ctx, pairPrefix+accessJti)
	pipe.Del(ctx, rpairPrefix+refreshJti)
	_, err := pipe.Exec(ctx)
	return err
}
