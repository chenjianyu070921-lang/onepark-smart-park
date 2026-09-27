package logic

// 未读计数 Redis 缓存(P1 高频读优化):
// GET /api/notices/unread-count 是前端轮询/横幅高频接口, 每次都 COUNT notice_read
// 送达记录, 在用户量与轮询频率上来后 DB 压力集中. 本文件提供读穿缓存(60s TTL):
//   - 读: 先查 Redis, 命中直接返回; 未命中回落 DB COUNT 并回填缓存;
//   - 失效: 已读回填成功后主动删除(见 marknoticereadlogic), 保证"用户读完立即减一";
//     新站内信送达依赖 60s TTL 自然过期(新消息延迟最多 60s 可接受).
//
// 一致性权衡: 缓存不可用时静默降级为直查 DB(不阻断主流程); 主动失效失败也由
// TTL 兜底, 最长 60s 不一致.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"onepark/common/redisx"

	"github.com/zeromicro/go-zero/core/logx"
)

// unreadCacheTTL 未读计数缓存 TTL(看板任务约定 60s):
// 新消息送达靠 TTL 自然过期, 已读回填靠主动删除, TTL 仅兜底失效失败等异常路径.
const unreadCacheTTL = 60 * time.Second

// unreadCacheKey 未读计数缓存键: 按 (租户, 用户) 隔离, 防止跨租户串数.
func unreadCacheKey(tenantID, uid int64) string {
	return fmt.Sprintf("notice:unread:cnt:%d:%d", tenantID, uid)
}

// getUnreadCache 读未读计数缓存.
// 返回 (值, true) 表示命中; 未命中/缓存不可用/值损坏均返回 (0, false) 回落 DB.
func getUnreadCache(ctx context.Context, r *redisx.Client, tenantID, uid int64) (int64, bool) {
	if r == nil {
		return 0, false
	}
	v, err := r.Get(ctx, unreadCacheKey(tenantID, uid)).Result()
	if err != nil {
		return 0, false // redis.Nil(未命中)或连接错误: 一律回落 DB
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false // 值损坏防御: 回落 DB 并让下次写入覆盖
	}
	return n, true
}

// setUnreadCache 回填未读计数缓存(静默失败: 缓存不可用不影响接口返回).
func setUnreadCache(ctx context.Context, r *redisx.Client, tenantID, uid int64, unread int64) {
	if r == nil {
		return
	}
	if err := r.Set(ctx, unreadCacheKey(tenantID, uid), unread, unreadCacheTTL).Err(); err != nil {
		logx.WithContext(ctx).Errorf("set unread cache failed: tenant=%d uid=%d err=%v", tenantID, uid, err)
	}
}

// invalidateUnreadCache 删除指定用户未读计数缓存(已读回填成功后调用).
// 静默失败: 删失败由 60s TTL 兜底, 不阻断已读回填主流程.
func invalidateUnreadCache(ctx context.Context, r *redisx.Client, tenantID, uid int64) {
	if r == nil {
		return
	}
	if err := r.Del(ctx, unreadCacheKey(tenantID, uid)).Err(); err != nil {
		logx.WithContext(ctx).Errorf("invalidate unread cache failed: tenant=%d uid=%d err=%v", tenantID, uid, err)
	}
}
