package logic

import (
	"context"

	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
	"onepark/common/errorx"
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
	// 0. 租户上下文: 只能看到本园区的账单
	tenantID, err := tenantOf(l.ctx)
	if err != nil {
		return nil, err
	}

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误(M2-E-5001)避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

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
	list, total, err := l.svcCtx.Billing.ListBill(l.ctx, tenantID, req.ZoneId, status, page, pageSize)
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
