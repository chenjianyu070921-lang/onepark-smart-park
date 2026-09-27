package logic

import (
	"context"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/rbac"

	"github.com/zeromicro/go-zero/core/logx"
)

// maxPageSize 分页查询单页上限.
const maxPageSize = 200

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

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 直接访问会空指针 panic(500).
	// 这里提前返回明确业务错误(M2-E-5001), 便于前端识别而非崩溃.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	// 统一在 WHERE 上追加 tenant_id, 保证 RBAC 行级隔离.
	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.WorkOrder{}).Where("tenant_id=?", tenantID)

	// RBAC 行级隔离: 受限角色(维修/业主)仅能查看与自己关联的工单, 全量角色看园区全部.
	// 维修 → 仅自己接的单(assignee_id); 业主 → 仅自己报的单(reporter_id).
	uid := ctxdata.GetUserId(l.ctx)
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if !rbac.IsFullScope(roles) {
		switch {
		case rbac.HasRole(roles, rbac.RoleRepair):
			q = q.Where("assignee_id=?", uid)
		case rbac.HasRole(roles, rbac.RoleOwner):
			q = q.Where("reporter_id=?", uid)
		default:
			q = q.Where("1=0") // 无对应范围角色, 拒绝查看
		}
	}

	// Status 为指针: nil 表示"未传筛选"(返回全部), 非 nil 按指针对应的合法状态精确筛选,
	// 含 status=0(待派单) —— 修复此前用 0 当哨兵导致待派单无法筛选的问题.
	if req.Status != nil {
		q = q.Where("status=?", *req.Status)
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
	// 单页上限, 防止超大 pageSize 深翻页拖库(与 dispatch-service maxPageSize 口径一致).
	if size > maxPageSize {
		size = maxPageSize
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
