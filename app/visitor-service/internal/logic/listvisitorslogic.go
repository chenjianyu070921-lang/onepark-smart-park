package logic

import (
	"context"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListVisitorsLogic 访客记录分页查询逻辑(强制租户隔离).
type ListVisitorsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListVisitorsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListVisitorsLogic {
	return &ListVisitorsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListVisitors 分页返回访客列表项.
func (l *ListVisitorsLogic) ListVisitors(req *types.ListVisitorReq) (resp *types.VisitorListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).Where("tenant_id=?", tenantID)
	if req.Status != 0 {
		q = q.Where("status=?", req.Status)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count visitor records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计访客失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.VisitorRecord
	if e := q.Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list visitor records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询访客失败")
	}

	items := make([]types.VisitorItem, 0, len(list))
	for _, v := range list {
		items = append(items, types.VisitorItem{
			Id:           v.ID,
			VisitorName:  v.VisitorName,
			VisitorPhone: v.VisitorPhone,
			Status:       v.Status,
			VisitTime:    timeOrZero(v.VisitTime),
			CheckinAt:    timeOrZero(v.CheckinAt),
			CheckoutAt:   timeOrZero(v.CheckoutAt),
			DeviceID:     v.DeviceID,
		})
	}

	return &types.VisitorListResp{Total: total, List: items}, nil
}

// timeOrZero 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timeOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
