package logic

import (
	"context"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListWorkOrdersLogic 工单分页查询逻辑(支持状态/类型/处理人筛选, 强制租户隔离).
type ListWorkOrdersLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListWorkOrdersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListWorkOrdersLogic {
	return &ListWorkOrdersLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListWorkOrders 分页返回工单列表项, 列表项不含大字段.
func (l *ListWorkOrdersLogic) ListWorkOrders(req *types.ListWorkOrderReq) (resp *types.WorkOrderListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 统一在 WHERE 上追加 tenant_id, 保证 RBAC 行级隔离.
	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.WorkOrder{}).Where("tenant_id=?", tenantID)
	if req.Status != 0 {
		q = q.Where("status=?", req.Status)
	}
	if req.Type != 0 {
		q = q.Where("type=?", req.Type)
	}
	if req.AssigneeID != 0 {
		q = q.Where("assignee_id=?", req.AssigneeID)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count work orders failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计工单失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.WorkOrder
	if e := q.Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list work orders failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询工单失败")
	}

	items := make([]types.WorkOrderItem, 0, len(list))
	for _, wo := range list {
		items = append(items, types.WorkOrderItem{
			Id:         wo.ID,
			OrderNo:    wo.OrderNo,
			Type:       wo.Type,
			Title:      wo.Title,
			Status:     wo.Status,
			Priority:   wo.Priority,
			AssigneeID: wo.AssigneeID,
			CreatedAt:  wo.CreatedAt.Unix(),
		})
	}

	return &types.WorkOrderListResp{Total: total, List: items}, nil
}
