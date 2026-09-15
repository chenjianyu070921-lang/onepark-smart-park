package logic

import (
	"context"
	"time"

	"onepark/app/dashboard-service/internal/types"
	workorderpb "onepark/proto/workorder"
)

// sourceTimeout 单数据源(工单 gRPC)调用超时, 避免下游不可用时拖垮接口.
const sourceTimeout = 800 * time.Millisecond

// cacheTimeout Redis 读写超时, 不可用时快速失败, 避免阻塞接口导致 503.
const cacheTimeout = 200 * time.Millisecond

// fetchWorkOrderStat 调用 workorder gRPC ListWorkOrders 聚合出大屏所需工单统计.
// 内部对传入 context 追加超时保护, 调用方无需再包装.
func fetchWorkOrderStat(ctx context.Context, client workorderpb.WorkorderServiceClient, tenant int64) (*types.WorkOrderStat, error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	r, err := client.ListWorkOrders(ctx, &workorderpb.ListWorkOrdersReq{
		TenantId: tenant,
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		return nil, err
	}
	return &types.WorkOrderStat{
		TenantId:          tenant,
		Total:             r.Total,
		TodayCount:        r.TodayCount,
		PendingCount:      r.PendingCount,
		CompletedToday:    r.CompletedToday,
		AvgProcessMinutes: r.AvgProcessMinutes,
		CompletionRate:    r.CompletionRate,
	}, nil
}
