// Package rpcserver 实现 workorder gRPC 服务(M5 运营调度大屏聚合依赖).
// 端口规划 9091; 提供 Ping 探活与 ListWorkOrders 聚合查询.
package rpcserver

import (
	"context"

	"onepark/app/workorder-service/internal/model"
	"onepark/common/gormx"
	commonpb "onepark/proto/common"
	workorderpb "onepark/proto/workorder"
)

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

// ListWorkOrders 供 M5 大屏聚合查询工单摘要列表、总数与待处理数.
// 入参: tenant_id 园区过滤(0 不限), status 状态过滤(0 不限), page/page_size 分页.
// 返回: 摘要列表 + 符合条件总数 + 待处理(待派单+处理中)工单总数.
func (s *WorkorderServer) ListWorkOrders(ctx context.Context, req *workorderpb.ListWorkOrdersReq) (*workorderpb.ListWorkOrdersResp, error) {
	resp := &workorderpb.ListWorkOrdersResp{}

	// DB 未初始化(本地无 MySQL)时返回空, 保证服务可启动用于联调.
	if s.DB == nil {
		return resp, nil
	}

	base := s.DB.WithContext(ctx).Model(&model.WorkOrder{})
	if req.TenantId != 0 {
		base = base.Where("tenant_id=?", req.TenantId)
	}
	if req.Status != 0 {
		base = base.Where("status=?", req.Status)
	}

	// 总数.
	var total int64
	base.Count(&total)
	resp.Total = total

	// 分页.
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	var list []model.WorkOrder
	base.Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list)

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

	// 待处理数: 待派单(0)+处理中(1).
	pendingDB := s.DB.WithContext(ctx).Model(&model.WorkOrder{})
	if req.TenantId != 0 {
		pendingDB = pendingDB.Where("tenant_id=?", req.TenantId)
	}
	var pending int64
	pendingDB.Where("status IN (?)", []int8{0, 1}).Count(&pending)
	resp.PendingCount = pending

	return resp, nil
}
