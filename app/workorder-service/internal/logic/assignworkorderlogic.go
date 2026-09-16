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
	// 处理人必须指定, 防止"处理中却无人负责"的僵尸工单; 部门可选(0 表示未指定).
	if req.AssigneeID <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "处理人不能为空")
	}

	// 乐观锁更新与派单流水同事务, 冲突或流水失败均整体回滚.
	now := time.Now()
	flow := &model.WorkOrderFlow{WorkOrderID: wo.ID, FromStatus: wo.Status, ToStatus: next, Action: state.ActionAssign, OperatorID: operatorID}
	flow.TenantID = tenantID
	flow.CreatedAt = now
	flow.UpdatedAt = now

	var conflict bool
	if e := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.WorkOrder{}).
			Where("id=? AND tenant_id=? AND version=?", wo.ID, tenantID, wo.Version).
			Updates(map[string]interface{}{
				"assignee_id":   req.AssigneeID,
				"department_id": req.DepartmentID,
				"status":        next,
				"version":       gorm.Expr("version+1"),
				"updated_at":    now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			conflict = true
			return nil
		}
		return tx.Create(flow).Error
	}); e != nil {
		l.Errorf("assign work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "派单失败")
	}
	if conflict {
		return nil, errorx.NewError(errorx.ErrWorkOrderAssignConflict, "派单冲突, 请刷新后重试")
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
