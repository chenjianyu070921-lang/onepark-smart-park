package logic

import (
	"context"
	"time"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// GetWorkOrderLogic 工单详情查询逻辑.
type GetWorkOrderLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetWorkOrderLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetWorkOrderLogic {
	return &GetWorkOrderLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetWorkOrder 按主键+租户查询工单详情(含全部字段与时间戳).
func (l *GetWorkOrderLogic) GetWorkOrder(req *types.IdReq) (resp *types.WorkOrderDetailResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	var wo model.WorkOrder
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", req.Id, tenantID).First(&wo).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrWorkOrderNotFound, "工单不存在")
		}
		l.Errorf("load work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载工单失败")
	}

	return &types.WorkOrderDetailResp{
		Id:           wo.ID,
		TenantID:     wo.TenantID,
		OrderNo:      wo.OrderNo,
		Type:         wo.Type,
		Title:        wo.Title,
		Description:  wo.Description,
		ReporterID:   wo.ReporterID,
		AssigneeID:   wo.AssigneeID,
		DepartmentID: wo.DepartmentID,
		Status:       wo.Status,
		Priority:     wo.Priority,
		Location:     wo.Location,
		Attachments:  wo.Attachments,
		Version:      wo.Version,
		CreatedAt:    wo.CreatedAt.Unix(),
		UpdatedAt:    wo.UpdatedAt.Unix(),
		FinishedAt:   timePtrUnix(wo.FinishedAt),
	}, nil
}

// timePtrUnix 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timePtrUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
