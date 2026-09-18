package dashboard

import (
	"context"

	"onepark/app/dashboard-service/internal/svc"
)

// overviewCachePattern 概览缓存的 key 模板(所有租户).
//
// 与 overviewCacheKey 拼出的格式保持一致: m5:dashboard:overview:tenant:{id}
const overviewCachePattern = "m5:dashboard:overview:tenant:*"

// scanBatch SCAN 每批的参考条数(不是上限, Redis 可能一次多返回).
const scanBatch = 100

// InvalidateOverviewCache 使概览缓存整体失效, 返回实际删除的 key 数量.
//
// 事件驱动(见 internal/wsserver/event.go)在推送增量前后调用它:
// 只推送事件而不管缓存的话, 下一次快照仍会读到旧聚合值,
// 事件的影响会被 30s 缓存盖住 —— 那"事件驱动"就白做了。
//
// 为什么按"所有租户"整体失效而不是精确到某个租户:
// Kafka 事件体里没有可靠的 tenant_id(common.proto 的 AlarmEvent 只有
// device_id/event_type), 而大屏本就是园区级视角, 事件影响的就是园区聚合值。
//
// 为什么用 SCAN 而不是 KEYS: KEYS 会在 Redis 单线程上遍历全库并阻塞其它命令,
// key 一多就是生产事故。SCAN 是增量游标遍历, 不阻塞。
func InvalidateOverviewCache(ctx context.Context, svcCtx *svc.ServiceContext) (int, error) {
	if svcCtx == nil || svcCtx.Redis == nil {
		// 未接 Redis 时缓存本来就不生效, 直接返回而不是报错
		return 0, nil
	}

	deleted := 0
	var cursor uint64
	for {
		keys, next, err := svcCtx.Redis.Scan(ctx, cursor, overviewCachePattern, scanBatch).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, err := svcCtx.Redis.Del(ctx, keys...).Result()
			if err != nil {
				return deleted, err
			}
			deleted += int(n)
		}
		cursor = next
		if cursor == 0 {
			return deleted, nil
		}
	}
}
