package logic

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

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
func (l *CreateWorkOrderLogic) CreateWorkOrder(req *types.CreateWorkOrderReq) (resp *types.WorkOrderResp, err error) {
	// 从网关上下文取租户ID(RBAC 隔离)与发起人ID.
	tenantID := ctxdata.GetTenantId(l.ctx)
	reporterID := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	// 生成工单号 WO-YYYYMMDD-XXXX.
	orderNo := genOrderNo()

	now := time.Now()
	wo := &model.WorkOrder{
		OrderNo:     orderNo,
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

	// 写入主表.
	if e := l.svcCtx.DB.WithContext(l.ctx).Create(wo).Error; e != nil {
		l.Errorf("create work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建工单失败")
	}

	// 写入建单流水(审计), action="create" 仅作记录, 不参与 FSM 流转.
	flow := &model.WorkOrderFlow{
		WorkOrderID: wo.ID,
		FromStatus:  -1,
		ToStatus:    state.StatusPendingDispatch,
		Action:      "create",
		OperatorID:  reporterID,
	}
	flow.TenantID = tenantID
	flow.CreatedAt = now
	flow.UpdatedAt = now
	_ = l.svcCtx.DB.WithContext(l.ctx).Create(flow).Error

	return &types.WorkOrderResp{
		Id:      wo.ID,
		OrderNo: wo.OrderNo,
		Status:  wo.Status,
	}, nil
}

// genOrderNo 生成工单号: WO-日期-4位随机, 简单防重(并发量低场景足够).
func genOrderNo() string {
	return fmt.Sprintf("WO-%s-%04d", time.Now().Format("20060102"), rand.Intn(10000))
}
