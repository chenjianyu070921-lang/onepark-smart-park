// Package rpcserver 实现 workorder gRPC 服务(M5 运营调度大屏聚合依赖).
// 端口规划 9091; 提供 Ping 探活与 ListWorkOrders 聚合查询.
package rpcserver

import (
	"context"
	"errors"
	"time"

	"onepark/app/workorder-service/internal/model"
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

	// 总数.
	var total int64
	s.scoped(ctx, tenant).Count(&total)
	resp.Total = total

	// 待处理数: 待派单(0)+处理中(1).
	var pending int64
	s.scoped(ctx, tenant).Where("status IN (?)", []int8{0, 1}).Count(&pending)
	resp.PendingCount = pending

	// 今日零点(本地时区).
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 今日新建.
	var todayCount int64
	s.scoped(ctx, tenant).Where("created_at >= ?", startOfDay).Count(&todayCount)
	resp.TodayCount = todayCount

	// 今日完成(已完成状态且 finished_at 在今日).
	var completedToday int64
	s.scoped(ctx, tenant).Where("status = ? AND finished_at >= ?", int8(3), startOfDay).Count(&completedToday)
	resp.CompletedToday = completedToday

	// 平均处理时长: 已完成工单 (finished_at - created_at) 的分钟均值.
	var avgMinutes float64
	s.scoped(ctx, tenant).
		Where("status = ? AND finished_at IS NOT NULL", int8(3)).
		Select("AVG(TIMESTAMPDIFF(MINUTE, created_at, finished_at))").
		Scan(&avgMinutes)
	if avgMinutes < 0 || s.DB == nil {
		avgMinutes = 0
	}
	resp.AvgProcessMinutes = avgMinutes

	// 完成率: 已完成(状态3)/总数 * 100.
	if total > 0 {
		var done int64
		s.scoped(ctx, tenant).Where("status = ?", int8(3)).Count(&done)
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
	var list []model.WorkOrder
	s.scoped(ctx, tenant).Where(statusFilter(req.Status)).
		Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list)

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

// statusFilter 生成状态过滤条件; status=0 表示不限.
func statusFilter(status int32) string {
	if status == 0 {
		return "1=1"
	}
	return "status = " + itoa(int(status))
}

// itoa 轻量整型转字符串, 避免引入 strconv 仅用于此场景.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
