package provider

import (
	"context"
	"time"

	workorderpb "onepark/proto/workorder"
)

// 工单状态码, 对齐 M2 app/workorder-service/internal/state/fsm.go:
// 0 待派单 / 1 处理中 / 2 待验收 / 3 已完成 / 4 已关闭.
const (
	woStatusCompleted int8 = 3
	woStatusClosed    int8 = 4
)

// workOrderListPageSize 是统计"今日新增工单数"时单次拉取的列表页大小.
const workOrderListPageSize = 500

// WorkOrder 是基于 M2 workorder-service gRPC 契约的真实实现.
type WorkOrder struct {
	client workorderpb.WorkorderServiceClient
}

// NewWorkOrder 用 M2 的 gRPC 客户端构造工单数据源.
func NewWorkOrder(client workorderpb.WorkorderServiceClient) *WorkOrder {
	return &WorkOrder{client: client}
}

// Stat 聚合出大屏工单卡片的指标, 全部来自 M2 gRPC 的真实返回值, 无任何硬编码.
//
//   - TodayTotal   今日新增: 列表页中 created_at 落在今日的条数
//   - Unfinished   未完成:  ListWorkOrdersResp.pending_count(待派单 + 处理中)
//   - CompleteRate 完成率:  (已完成 + 已关闭) / 总数
//   - AvgHandleSec 平均处理时长: M2 契约未暴露 finished_at, 返回 nil 表示暂不可得
func (p *WorkOrder) Stat(ctx context.Context, tenantId int64) (WorkOrderStat, error) {
	all, err := p.client.ListWorkOrders(ctx, &workorderpb.ListWorkOrdersReq{
		TenantId: tenantId,
		Status:   0,
		Page:     1,
		PageSize: workOrderListPageSize,
	})
	if err != nil {
		return WorkOrderStat{}, err
	}

	stat := WorkOrderStat{
		Unfinished: all.GetPendingCount(),
		TodayTotal: countCreatedToday(all.GetList()),
	}

	completed, err := p.countByStatus(ctx, tenantId, woStatusCompleted)
	if err != nil {
		return WorkOrderStat{}, err
	}
	closed, err := p.countByStatus(ctx, tenantId, woStatusClosed)
	if err != nil {
		return WorkOrderStat{}, err
	}
	if total := all.GetTotal(); total > 0 {
		stat.CompleteRate = float64(completed+closed) / float64(total)
	}

	return stat, nil
}

// countByStatus 按状态过滤取总数, page_size 取 1 只复用 total 字段.
func (p *WorkOrder) countByStatus(ctx context.Context, tenantId int64, status int8) (int64, error) {
	resp, err := p.client.ListWorkOrders(ctx, &workorderpb.ListWorkOrdersReq{
		TenantId: tenantId,
		Status:   int32(status),
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		return 0, err
	}
	return resp.GetTotal(), nil
}

// countCreatedToday 统计列表页中创建时间落在今日的工单数.
// 注意: M2 契约不支持按日期过滤, 当日工单数超过 workOrderListPageSize 时会偏小.
func countCreatedToday(list []*workorderpb.WorkOrderSummary) int64 {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	var n int64
	for _, it := range list {
		if it.GetCreatedAt() >= todayStart {
			n++
		}
	}
	return n
}
