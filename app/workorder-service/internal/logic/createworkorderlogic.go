package logic

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
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
	// 入参白名单校验, 防止脏类型/脏优先级入库.
	if strings.TrimSpace(req.Title) == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "工单标题不能为空")
	}
	if !model.ValidType(req.Type) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "工单类型非法, 仅支持 1-7")
	}
	if !model.ValidPriority(req.Priority) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "优先级非法, 仅支持 1-3")
	}

	// 生成工单号 WO-YYYYMMDD-XXXX; 建单与流水同事务.
	// 单号是 4 位随机数, 同秒并发可能碰撞: 捕获唯一键冲突后换号重试(上限 5 次).
	var wo *model.WorkOrder
	var flow *model.WorkOrderFlow
	var e error
	for attempt := 0; attempt < 5; attempt++ {
		orderNo := genOrderNo()
		now := time.Now()
		wo = &model.WorkOrder{
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

		// 主表与建单流水同事务, 防止审计断档; 流水失败即整体回滚, 由客户端重试.
		flow = &model.WorkOrderFlow{
			WorkOrderID: wo.ID,
			FromStatus:  -1,
			ToStatus:    state.StatusPendingDispatch,
			Action:      "create",
			OperatorID:  reporterID,
		}
		flow.TenantID = tenantID
		flow.CreatedAt = now
		flow.UpdatedAt = now

		e = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
			if e := tx.Create(wo).Error; e != nil {
				return e
			}
			return tx.Create(flow).Error
		})
		if e == nil {
			break
		}
		if isDuplicateEntry(e) {
			// 单号碰撞: 换号重试, 不视为失败
			l.Infof("work order no collision, retry attempt=%d", attempt+1)
			continue
		}
		break
	}
	if e != nil {
		l.Errorf("create work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建工单失败")
	}

	// 发布建单事件(workorder-event), 供 M5 大屏/通知类消费; 失败仅记日志不阻断建单.
	// 注意: 事件必须在事务提交成功后发布, 避免下游收到回滚数据的幻影事件.
	publishWorkOrderEvent(l.ctx, l.svcCtx, l.Logger, WorkOrderEvent{
		Event:       "created",
		Action:      "create",
		TenantId:    tenantID,
		WorkOrderId: wo.ID,
		OrderNo:     wo.OrderNo,
		FromStatus:  -1,
		ToStatus:    state.StatusPendingDispatch,
		OperatorId:  reporterID,
		Timestamp:   wo.CreatedAt.Unix(),
	})

	return &types.WorkOrderResp{
		Id:      wo.ID,
		OrderNo: wo.OrderNo,
		Status:  wo.Status,
	}, nil
}

// genOrderNo 生成工单号: WO-日期-4位随机.
// 唯一性由 uk_order_no 兜底; 高并发同秒碰撞时, 建单入口捕获唯一键冲突后换号重试.
func genOrderNo() string {
	return fmt.Sprintf("WO-%s-%04d", time.Now().Format("20060102"), rand.Intn(10000))
}

// isDuplicateEntry 判断 MySQL 唯一键冲突(1062), 用于单号碰撞重试.
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
