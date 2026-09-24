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

	"github.com/zeromicro/go-zero/core/logx"
)

// CreateWorkOrderLogic 创建工单逻辑: 报修/投诉/巡检/保洁/装修/搬运/其他.
// 建单即"待派单"(StatusPendingDispatch=0), 处理人默认0, 乐观锁版本默认0.
type CreateWorkOrderLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateWorkOrderLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateWorkOrderLogic {
	return &CreateWorkOrderLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CreateWorkOrder 处理创建工单请求, 写入 work_order 主表与"建单"流水.
// 入参: req 含工单类型/标题/描述/优先级/位置; 租户与发起人取自网关注入的上下文.
// 返回: 工单主键/工单号/当前状态.
//
// 一致性保证(修复此前"主表 Create 后流水 _ = 吞错"的问题):
//   - 主表与流水在**同一事务**写入, 流水失败则整单回滚, 不会出现"有单无流水"的审计空洞;
//   - 工单号是随机生成, 撞 uk_order_no 唯一键时由 RetryOnDuplicate 换号重建.
func (l *CreateWorkOrderLogic) CreateWorkOrder(req *types.CreateWorkOrderReq) (resp *types.WorkOrderResp, err error) {
	// 从网关上下文取租户ID(RBAC 隔离)与发起人ID.
	tenantID := ctxdata.GetTenantId(l.ctx)
	reporterID := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	// 枚举合法性校验: Type/Priority 为受控枚举(见 model.ValidType/ValidPriority),
	// 此前建单直接透传, 非法值(如 0/99)会落库导致看板聚合与前端展示出现无法识别的类型/优先级.
	if !model.ValidType(req.Type) {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "工单类型不合法(1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他)")
	}
	if !model.ValidPriority(req.Priority) {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "工单优先级不合法(1紧急 2普通 3低)")
	}

	now := time.Now()
	wo := &model.WorkOrder{
		OrderNo:     "", // 事务内生成, 与撞号重试配合
		Type:        req.Type,
		Title:       req.Title,
		Description: req.Description,
		ReporterID:  reporterID,
		Status:      state.StatusPendingDispatch,
		Priority:    req.Priority,
		Location:    req.Location,
		Version:     0,
	}
	wo.TenantID = tenantID
	wo.CreatedAt = now
	wo.UpdatedAt = now

	// 主表+流水同事务, 撞号(1062)自动换号重试; 非冲突错误原样返回.
	err = model.RetryOnDuplicate(model.MaxOrderNoRetries, func() error {
		return l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gormx.DB) error {
			wo.OrderNo = model.NewOrderNo()
			if e := tx.Create(wo).Error; e != nil {
				return e
			}
			return tx.Create(buildCreateFlow(tenantID, reporterID, wo, now)).Error
		})
	})
	if err != nil {
		l.Errorf("create work order failed: orderNo=%s, err=%v", wo.OrderNo, err)
		if model.IsDuplicateEntry(err) {
			return nil, errorx.NewError(errorx.ErrM2Internal, "工单号生成冲突, 请重试")
		}
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建工单失败")
	}

	// 发布建单事件(workorder-event), 供 M5 大屏/通知类消费; 失败仅记日志不阻断建单.
	publishWorkOrderEvent(l.ctx, l.svcCtx, l.Logger, WorkOrderEvent{
		Event:       "created",
		Action:      state.ActionCreate,
		TenantId:    tenantID,
		WorkOrderId: wo.ID,
		OrderNo:     wo.OrderNo,
		FromStatus:  -1,
		ToStatus:    state.StatusPendingDispatch,
		OperatorId:  reporterID,
		Timestamp:   now.Unix(),
	})

	return &types.WorkOrderResp{
		Id:      wo.ID,
		OrderNo: wo.OrderNo,
		Status:  wo.Status,
	}, nil
}

// buildCreateFlow 构造建单流水(审计用, action=create 不参与 FSM 流转).
func buildCreateFlow(tenantID, reporterID int64, wo *model.WorkOrder, now time.Time) *model.WorkOrderFlow {
	flow := &model.WorkOrderFlow{
		WorkOrderID: wo.ID,
		FromStatus:  -1,
		ToStatus:    state.StatusPendingDispatch,
		Action:      state.ActionCreate,
		OperatorID:  reporterID,
	}
	flow.TenantID = tenantID
	flow.CreatedAt = now
	flow.UpdatedAt = now
	return flow
}
