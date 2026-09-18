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

// UpdateWorkOrderStatusLogic 工单状态流转逻辑: submit/approve/reject/close.
// 任何活跃态均可 close; 驳回(reject)回到处理中; 验收通过(approve)置 finished_at.
type UpdateWorkOrderStatusLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateWorkOrderStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateWorkOrderStatusLogic {
	return &UpdateWorkOrderStatusLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// UpdateWorkOrderStatus 处理状态流转请求.
// 入参: req.Id 工单ID(路径), req.Action 动作, req.Remark 备注(可选).
// 返回: 工单主键/工单号/流转后状态.
func (l *UpdateWorkOrderStatusLogic) UpdateWorkOrderStatus(req *types.UpdateWorkOrderStatusReq) (resp *types.WorkOrderResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	operatorID := ctxdata.GetUserId(l.ctx)

	var wo model.WorkOrder
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", req.Id, tenantID).First(&wo).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrWorkOrderNotFound, "工单不存在")
		}
		l.Errorf("load work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载工单失败")
	}

	// RBAC 写权限: 维修仅能操作自己接的单, 业主仅能操作自己报的单; 全量角色不受限.
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if rbac.HasRole(roles, rbac.RoleRepair) && wo.AssigneeID != operatorID {
		return nil, errorx.NewError(errorx.ErrForbidden, "仅能操作自己接的单")
	}
	if rbac.HasRole(roles, rbac.RoleOwner) && wo.ReporterID != operatorID {
		return nil, errorx.NewError(errorx.ErrForbidden, "仅能操作自己报的单")
	}

	// FSM 校验目标状态.
	next, ok := state.NextStatus(wo.Status, req.Action)
	if !ok {
		return nil, errorx.NewError(errorx.ErrWorkOrderStatusInvalid, "当前状态不允许执行该动作")
	}

	now := time.Now()
	updates := map[string]interface{}{
		"status":     next,
		"version":    gorm.Expr("version+1"),
		"updated_at": now,
	}
	// 终态(已完成/已关闭)写入完成时间.
	if next == state.StatusCompleted || next == state.StatusClosed {
		updates["finished_at"] = now
	}

	// 乐观锁更新与流转流水在**同一事务**执行(修复此前流水 _ = 吞错导致"状态已变但无审计流水"的问题):
	//   - version 条件不满足(RowsAffected==0) → 返回业务错误 → 事务回滚;
	//   - 流水写入失败 → 返回错误 → 状态更新一并回滚.
	res := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gormx.DB) error {
		r := tx.WithContext(l.ctx).
			Model(&model.WorkOrder{}).
			Where("id=? AND tenant_id=? AND version=?", wo.ID, tenantID, wo.Version).
			Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			// 乐观锁冲突: 版本已被其它请求改走, 回滚本次流转.
			return errorx.NewError(errorx.ErrWorkOrderStatusConflict, "状态流转冲突, 请刷新后重试")
		}

		flow := &model.WorkOrderFlow{WorkOrderID: wo.ID, FromStatus: wo.Status, ToStatus: next, Action: req.Action, OperatorID: operatorID, Remark: req.Remark}
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
		l.Errorf("update work order status failed: %v", res)
		return nil, errorx.NewError(errorx.ErrM2Internal, "状态流转失败")
	}

	// 发布状态流转事件(workorder-event); 失败仅记日志不阻断流转.
	publishWorkOrderEvent(l.ctx, l.svcCtx, l.Logger, WorkOrderEvent{
		Event:       "status_changed",
		Action:      req.Action,
		TenantId:    tenantID,
		WorkOrderId: wo.ID,
		OrderNo:     wo.OrderNo,
		FromStatus:  wo.Status,
		ToStatus:    next,
		OperatorId:  operatorID,
		AssigneeId:  wo.AssigneeID,
		Timestamp:   now.Unix(),
	})

	return &types.WorkOrderResp{Id: wo.ID, OrderNo: wo.OrderNo, Status: next}, nil
}
