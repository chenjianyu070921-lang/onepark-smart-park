package logic

import (
	"context"

	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

const (
	// defaultPageSize 每页默认条数
	defaultPageSize = 10
	// maxPageSize 每页最多条数, 防止有人传 pageSize=99999 把库拖垮
	maxPageSize = 100
)

// BillListLogic 接口62: 账单列表
type BillListLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBillListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillListLogic {
	return &BillListLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *BillListLogic) BillList(req *types.BillListRequest) (*types.BillListResponse, error) {
	// 1. 分页参数兜底
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	status := StatusOf(req.Status)
	list, total, err := l.svcCtx.Billing.ListBill(l.ctx, req.ZoneId, status, page, pageSize)
	if err != nil {
		return nil, wrapErr("查询账单列表", err)
	}

	items := make([]types.BillItem, 0, len(list))
	for _, b := range list {
		items = append(items, types.BillItem{
			BillNo:      b.BillNo,
			ZoneId:      b.ZoneID,
			RuleId:      int64(b.RuleID),
			PeriodStart: b.PeriodStart.Format(timeLayoutDate),
			PeriodEnd:   b.PeriodEnd.Format(timeLayoutDate),
			UsageKwh:    round2(b.UsageKwh),
			Amount:      b.Amount,
			Status:      b.Status,
			CreatedAt:   b.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return &types.BillListResponse{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		List:     items,
	}, nil
}
