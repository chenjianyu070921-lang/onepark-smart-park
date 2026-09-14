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

	// 乐观锁更新.
	res := l.svcCtx.DB.WithContext(l.ctx).
		Model(&model.WorkOrder{}).
		Where("id=? AND tenant_id=? AND version=?", wo.ID, tenantID, wo.Version).
		Updates(updates)
	if res.Error != nil {
		l.Errorf("update work order status failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrM2Internal, "状态流转失败")
	}
	if res.RowsAffected == 0 {
		return nil, errorx.NewError(errorx.ErrWorkOrderAssignFailed, "状态流转冲突, 请刷新后重试")
	}

	// 写流转流水.
	flow := &model.WorkOrderFlow{WorkOrderID: wo.ID, FromStatus: wo.Status, ToStatus: next, Action: req.Action, OperatorID: operatorID, Remark: req.Remark}
	flow.TenantID = tenantID
	flow.CreatedAt = now
	flow.UpdatedAt = now
	_ = l.svcCtx.DB.WithContext(l.ctx).Create(flow).Error

	return &types.WorkOrderResp{Id: wo.ID, OrderNo: wo.OrderNo, Status: next}, nil
}
