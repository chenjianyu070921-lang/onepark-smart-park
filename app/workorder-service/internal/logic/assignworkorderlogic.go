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
	"onepark/common/gormx"
	"onepark/common/rbac"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// AssignWorkOrderLogic 派单逻辑: 待派单(0)→处理中(1), 分配处理人与部门.
// 使用乐观锁(Where version)防止并发派单, RowsAffected==0 即冲突.
type AssignWorkOrderLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAssignWorkOrderLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AssignWorkOrderLogic {
	return &AssignWorkOrderLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AssignWorkOrder 处理派单请求.
// 入参: req.Id 工单ID(路径), req.AssigneeID 处理人, req.DepartmentID 处理部门(可选).
// 返回: 工单主键/工单号/当前状态.
func (l *AssignWorkOrderLogic) AssignWorkOrder(req *types.AssignWorkOrderReq) (resp *types.WorkOrderResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	operatorID := ctxdata.GetUserId(l.ctx)

	// RBAC 写权限: 仅系统管理员/园区管理员/物业客服可派单, 受限角色(维修/业主)无权派单.
	if !rbac.CanManageWorkOrder(rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))) {
		return nil, errorx.NewError(errorx.ErrForbidden, "无派单权限")
	}

	// 先按租户+主键加载, 校验存在性与当前状态.
	var wo model.WorkOrder
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", req.Id, tenantID).First(&wo).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrWorkOrderNotFound, "工单不存在")
		}
		l.Errorf("load work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载工单失败")
	}

	// FSM 校验: 仅允许从待派单执行 assign.
	next, ok := state.NextStatus(wo.Status, state.ActionAssign)
	if !ok {
		return nil, errorx.NewError(errorx.ErrWorkOrderStatusInvalid, "当前状态不允许派单")
	}

	// 乐观锁更新与派单流水在**同一事务**执行(修复此前流水 _ = 吞错导致"派单成功但无流水"的问题):
	//   - version 条件不满足(RowsAffected==0) → 返回错误 → 事务回滚(此处无写操作, 天然安全);
	//   - 流水写入失败 → 返回错误 → 状态更新一并回滚, 不会出现"状态已变但无审计流水".
	now := time.Now()
	res := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gormx.DB) error {
		r := tx.WithContext(l.ctx).
			Model(&model.WorkOrder{}).
			Where("id=? AND tenant_id=? AND version=?", wo.ID, tenantID, wo.Version).
			Updates(map[string]interface{}{
				"assignee_id":   req.AssigneeID,
				"department_id": req.DepartmentID,
				"status":        next,
				"version":       gorm.Expr("version+1"),
				"updated_at":    now,
			})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			// 乐观锁冲突: 版本已被其它请求改走, 回滚本次派单.
			return errorx.NewError(errorx.ErrWorkOrderAssignConflict, "派单冲突, 请刷新后重试")
		}

		flow := &model.WorkOrderFlow{WorkOrderID: wo.ID, FromStatus: wo.Status, ToStatus: next, Action: state.ActionAssign, OperatorID: operatorID}
		flow.TenantID = tenantID
		flow.CreatedAt = now
		flow.UpdatedAt = now
		return tx.WithContext(l.ctx).Create(flow).Error
	})
	if res != nil {
		// 乐观锁冲突是 errorx.CodeError, 原样透出给前端; 其余按内部错误兜底.
		if ce, ok := res.(*errorx.CodeError); ok {
			return nil, ce
		}
		l.Errorf("assign work order failed: %v", res)
		return nil, errorx.NewError(errorx.ErrM2Internal, "派单失败")
	}

	// 发布派单事件(workorder-event); 失败仅记日志不阻断派单.
	publishWorkOrderEvent(l.ctx, l.svcCtx, l.Logger, WorkOrderEvent{
		Event:       "assigned",
		Action:      state.ActionAssign,
		TenantId:    tenantID,
		WorkOrderId: wo.ID,
		OrderNo:     wo.OrderNo,
		FromStatus:  wo.Status,
		ToStatus:    next,
		OperatorId:  operatorID,
		AssigneeId:  req.AssigneeID,
		Timestamp:   now.Unix(),
	})

	return &types.WorkOrderResp{Id: wo.ID, OrderNo: wo.OrderNo, Status: next}, nil
}
