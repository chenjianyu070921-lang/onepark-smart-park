package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"onepark/common/ctxdata"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	overviewCachePrefix = "dashboard:overview:"
	overviewCacheTTL    = 30 * time.Second
)

// OverviewLogic 首页概览聚合逻辑.
type OverviewLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewOverviewLogic(ctx context.Context, svcCtx *svc.ServiceContext) *OverviewLogic {
	return &OverviewLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Overview 返回首页概览: 已就绪数据源(工单)填充真实值, 未就绪字段为 null 并记降级标记.
// 概览结果按租户维度缓存 30 秒. Redis/数据源不可用时均快速降级, 接口始终 200.
func (l *OverviewLogic) Overview() (resp *types.OverviewResp, err error) {
	tenant := ctxdata.GetTenantId(l.ctx)
	resp = &types.OverviewResp{TenantId: tenant}

	// 1) 命中缓存(带租户维度, 短超时避免 Redis 不可用时阻塞).
	cacheKey := overviewCachePrefix + fmt.Sprintf("%d", tenant)
	rctx, rcancel := context.WithTimeout(l.ctx, cacheTimeout)
	defer rcancel()
	if data, cErr := l.svcCtx.Redis.Get(rctx, cacheKey).Bytes(); cErr == nil {
		if json.Unmarshal(data, resp) == nil {
			resp.Cached = true
			return resp, nil
		}
	}

	// 2) 聚合工单数据源(独立降级, 失败仅标记不抛 500).
	markers := make([]string, 0)
	wo, woErr := fetchWorkOrderStat(l.ctx, l.svcCtx.WorkorderRPC, tenant)
	if woErr != nil {
		l.Logger.Errorf("overview workorder degraded: %v", woErr)
		markers = append(markers, "workorder")
	}
	resp.WorkOrder = wo
	resp.FallbackMarkers = markers

	// 3) 写回缓存(含降级结果也缓存, 避免抖动放大; 失败忽略).
	if data, mErr := json.Marshal(resp); mErr == nil {
		wctx, wcancel := context.WithTimeout(l.ctx, cacheTimeout)
		defer wcancel()
		_ = l.svcCtx.Redis.Set(wctx, cacheKey, data, overviewCacheTTL).Err()
	}

	return resp, nil
}
