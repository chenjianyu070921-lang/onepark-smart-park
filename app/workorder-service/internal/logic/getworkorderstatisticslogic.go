package logic

import (
	"context"
	"time"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// GetWorkOrderStatisticsLogic 工单统计看板逻辑(P2).
type GetWorkOrderStatisticsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewGetWorkOrderStatisticsLogic 构造工单统计逻辑.
func NewGetWorkOrderStatisticsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetWorkOrderStatisticsLogic {
	return &GetWorkOrderStatisticsLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// GetWorkOrderStatistics 聚合统计: 总数/待处理/今日新建/今日完成/平均处理时长/完成率.
// 与 gRPC ListWorkOrders 共用同一套聚合口径(单次扫描), 供物业后台 HTTP 看板使用.
// 入参: 网关注入的 x-tenant-id(0 表示不限园区).
func (l *GetWorkOrderStatisticsLogic) GetWorkOrderStatistics() (*types.WorkOrderStatisticsResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}
	now := time.Now()
	// 今日零点(本地时区), 用于今日新建/今日完成统计.
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 单次聚合扫描: COUNT/SUM/AVG 替代多次串行统计; SUM/AVG 空表返回 NULL 用 COALESCE 归零.
	// 状态值用 state 常量参数化, 不在 SQL 中硬编码.
	var stats struct {
		Total          int64   `json:"total"`
		Pending        int64   `json:"pending"`
		TodayNew       int64   `json:"today_new"`
		CompletedToday int64   `json:"completed_today"`
		Done           int64   `json:"done"`
		AvgProcessMin  float64 `json:"avg_process_min"`
	}
	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.WorkOrder{})
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	// 完成率/今日完成需计入终态"已关闭"(status=4): FSM 允许 3→4 close, 关闭后若只计 status=3
	// 会让完成率随 close 回退、今日完成数下降(审查问题9). 故 done/completed_today/avg 均用 status IN (3,4).
	if err := q.Select(
		"COUNT(*) AS total, "+
			"COALESCE(SUM(status IN (?,?)), 0) AS pending, "+
			"COALESCE(SUM(created_at >= ?), 0) AS today_new, "+
			"COALESCE(SUM(status IN (?,?) AND finished_at >= ?), 0) AS completed_today, "+
			"COALESCE(SUM(status IN (?,?)), 0) AS done, "+
			"COALESCE(AVG(CASE WHEN status IN (?,?) AND finished_at IS NOT NULL THEN "+
			"TIMESTAMPDIFF(MINUTE, created_at, finished_at) END), 0) AS avg_process_min",
		state.StatusPendingDispatch, state.StatusProcessing,
		startOfDay,
		state.StatusCompleted, state.StatusClosed, startOfDay,
		state.StatusCompleted, state.StatusClosed,
		state.StatusCompleted, state.StatusClosed,
	).Scan(&stats).Error; err != nil {
		l.Errorf("workorder statistics failed: %v", err)
		return nil, errorx.NewError(errorx.ErrM2Internal, "工单统计查询失败")
	}

	resp := &types.WorkOrderStatisticsResp{
		Total:             stats.Total,
		PendingCount:      stats.Pending,
		TodayCount:        stats.TodayNew,
		CompletedToday:    stats.CompletedToday,
		AvgProcessMinutes: stats.AvgProcessMin,
	}
	// 完成率: 已完成(状态3)/总数 * 100.
	if stats.Total > 0 {
		resp.CompletionRate = float64(stats.Done) / float64(stats.Total) * 100
	}
	return resp, nil
}
