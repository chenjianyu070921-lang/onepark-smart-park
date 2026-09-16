// Package rpcserver 实现 workorder gRPC 服务(M5 运营调度大屏聚合依赖).
// 端口规划 9091; 提供 Ping 探活与 ListWorkOrders 聚合查询.
package rpcserver

import (
	"context"
	"errors"
	"time"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
	"onepark/common/errorx"
	"onepark/common/gormx"
	commonpb "onepark/proto/common"
	workorderpb "onepark/proto/workorder"
)

// ErrDBUninitialized 表示 gRPC 依赖的 MySQL 未初始化(DSN 未配置或连接失败).
// 显式返回错误而非静默空数据, 避免 M5 大屏误把 0 当成"真实统计".
var ErrDBUninitialized = errors.New("workorder db not initialized")

// WorkorderServer 工单 gRPC 服务实现.
type WorkorderServer struct {
	workorderpb.UnimplementedWorkorderServiceServer
	DB *gormx.DB // GORM MySQL 连接(未配置时为 nil, 返回空结果)
}

// NewWorkorderServer 构造工单 gRPC 服务.
func NewWorkorderServer(db *gormx.DB) *WorkorderServer {
	return &WorkorderServer{DB: db}
}

// Ping 探活.
func (s *WorkorderServer) Ping(ctx context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// scoped 返回已注入租户过滤的工单查询构造器. tenant_id=0 表示不限园区.
func (s *WorkorderServer) scoped(ctx context.Context, tenantId int64) *gormx.DB {
	q := s.DB.WithContext(ctx).Model(&model.WorkOrder{})
	if tenantId != 0 {
		q = q.Where("tenant_id = ?", tenantId)
	}
	return q
}

// ListWorkOrders 供 M5 大屏聚合查询工单摘要列表、总数与待处理数.
// 入参: tenant_id 园区过滤(0 不限), status 状态过滤(0 不限), page/page_size 分页.
// 返回: 摘要列表 + 符合条件总数 + 待处理(待派单+处理中)工单总数 +
// 今日新建/今日完成/平均处理时长/完成率等聚合指标.
func (s *WorkorderServer) ListWorkOrders(ctx context.Context, req *workorderpb.ListWorkOrdersReq) (*workorderpb.ListWorkOrdersResp, error) {
	resp := &workorderpb.ListWorkOrdersResp{}

	// DB 未初始化时显式返回错误, 让上游(dashboard)走降级而非拿到静默的 0.
	if s.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, ErrDBUninitialized.Error())
	}

	tenant := req.TenantId

	// 统计查询统一检查错误: 任一失败返回错误, 让上游(dashboard)走降级而非拿到静默的 0.
	// 总数.
	var total int64
	if err := s.scoped(ctx, tenant).Count(&total).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计工单总数失败")
	}
	resp.Total = total

	// 待处理数: 待派单(0)+处理中(1).
	var pending int64
	if err := s.scoped(ctx, tenant).
		Where("status IN (?)", []int8{state.StatusPendingDispatch, state.StatusProcessing}).
		Count(&pending).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计待处理工单失败")
	}
	resp.PendingCount = pending

	// 今日零点(本地时区).
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 今日新建.
	var todayCount int64
	if err := s.scoped(ctx, tenant).Where("created_at >= ?", startOfDay).Count(&todayCount).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计今日新建工单失败")
	}
	resp.TodayCount = todayCount

	// 今日完成(已完成状态且 finished_at 在今日).
	var completedToday int64
	if err := s.scoped(ctx, tenant).
		Where("status = ? AND finished_at >= ?", state.StatusCompleted, startOfDay).
		Count(&completedToday).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计今日完成工单失败")
	}
	resp.CompletedToday = completedToday

	// 平均处理时长: 已完成工单 (finished_at - created_at) 的分钟均值.
	var avgMinutes float64
	if err := s.scoped(ctx, tenant).
		Where("status = ? AND finished_at IS NOT NULL", state.StatusCompleted).
		Select("AVG(TIMESTAMPDIFF(MINUTE, created_at, finished_at))").
		Scan(&avgMinutes).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计平均处理时长失败")
	}
	if avgMinutes < 0 {
		avgMinutes = 0
	}
	resp.AvgProcessMinutes = avgMinutes

	// 完成率: 已完成(状态3)/总数 * 100.
	if total > 0 {
		var done int64
		if err := s.scoped(ctx, tenant).Where("status = ?", state.StatusCompleted).Count(&done).Error; err != nil {
			return nil, errorx.NewError(errorx.ErrM2Internal, "统计已完成工单失败")
		}
		resp.CompletionRate = float64(done) / float64(total) * 100
	}

	// 分页摘要列表.
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	listQ := s.scoped(ctx, tenant)
	if req.Status != 0 {
		listQ = listQ.Where("status = ?", int8(req.Status))
	}
	var list []model.WorkOrder
	if err := listQ.Order("id DESC").
		Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询工单列表失败")
	}

	summaries := make([]*workorderpb.WorkOrderSummary, 0, len(list))
	for _, wo := range list {
		summaries = append(summaries, &workorderpb.WorkOrderSummary{
			Id:         wo.ID,
			OrderNo:    wo.OrderNo,
			Type:       int32(wo.Type),
			Status:     int32(wo.Status),
			Priority:   int32(wo.Priority),
			AssigneeId: wo.AssigneeID,
			CreatedAt:  wo.CreatedAt.Unix(),
		})
	}
	resp.List = summaries

	return resp, nil
}
