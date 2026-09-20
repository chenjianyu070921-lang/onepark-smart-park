package provider

import (
	"context"

	workorderpb "onepark/proto/workorder"
)

// WorkOrderStat / WorkOrderProvider 端口声明见 provider.go(本文件仅含 M2 gRPC 实现).

// WorkOrder 是基于 M2 workorder-service gRPC 契约的真实实现.
type WorkOrder struct {
	client workorderpb.WorkorderServiceClient
}

// NewWorkOrder 用 M2 的 gRPC 客户端构造工单数据源.
func NewWorkOrder(client workorderpb.WorkorderServiceClient) *WorkOrder {
	return &WorkOrder{client: client}
}

// Stat 聚合出大屏工单卡片的指标.
//
// 统计数据对齐(2026-09-18 联调): M2 ListWorkOrders 已在服务端单次聚合扫描返回
// today_count/pending_count/avg_process_minutes/completion_rate 全部指标,
// 本端单次调用直采, 不再自行拉列表数数 —— 消除两个口径漂移:
//  1. 旧实现自算今日新增(受 pageSize=500 截断, 超过后偏小) vs M2 服务端精确聚合;
//  2. 旧实现完成率 = (已完成+已关闭)/总数, 与 M2 的 已完成/总数 口径不一致;
//     以 M2(数据归属方)为准, 本端只做 0-100 → 0-1 的单位换算.
func (p *WorkOrder) Stat(ctx context.Context, tenantId int64) (WorkOrderStat, error) {
	// PageSize=1: 只消费聚合指标字段, 不需要列表数据.
	all, err := p.client.ListWorkOrders(ctx, &workorderpb.ListWorkOrdersReq{
		TenantId: tenantId,
		Status:   0,
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		return WorkOrderStat{}, err
	}

	stat := WorkOrderStat{
		TodayTotal:   all.GetTodayCount(),
		Unfinished:   all.GetPendingCount(), // 待派单(0) + 处理中(1)
		CompleteRate: all.GetCompletionRate() / 100,
	}
	if m := all.GetAvgProcessMinutes(); m > 0 {
		sec := m * 60
		stat.AvgHandleSec = &sec
	}
	return stat, nil
}
