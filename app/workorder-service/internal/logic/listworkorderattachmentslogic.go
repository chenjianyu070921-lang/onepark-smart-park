package logic

import (
	"context"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// ListWorkOrderAttachmentsLogic 工单附件列表查询逻辑.
type ListWorkOrderAttachmentsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListWorkOrderAttachmentsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListWorkOrderAttachmentsLogic {
	return &ListWorkOrderAttachmentsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListWorkOrderAttachments 校验工单(租户隔离)后返回其全部附件明细(按上传时间倒序).
func (l *ListWorkOrderAttachmentsLogic) ListWorkOrderAttachments(req *types.ListWorkOrderAttachmentsReq) (resp *types.WorkOrderAttachmentListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	var wo model.WorkOrder
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", req.Id, tenantID).First(&wo).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrWorkOrderNotFound, "工单不存在")
		}
		l.Errorf("load work order failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载工单失败")
	}

	var atts []model.WorkOrderAttachment
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("work_order_id=?", wo.ID).Order("id desc").Find(&atts).Error; e != nil {
		l.Errorf("query attachments failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询附件失败")
	}

	list := make([]types.WorkOrderAttachmentItem, 0, len(atts))
	for _, a := range atts {
		list = append(list, types.WorkOrderAttachmentItem{
			Id:        a.ID,
			ObjectKey: a.ObjectKey,
			FileName:  a.FileName,
			CreatedAt: a.CreatedAt.Unix(),
		})
	}
	return &types.WorkOrderAttachmentListResp{List: list}, nil
}
