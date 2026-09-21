package lease

import (
	"context"

	"github.com/shopspring/decimal"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// BillSummaryLogic 租金账单汇总(应收 / 已收 / 欠费).
//
// 招商页要的就是这几个数, 此前没有任何接口能算出来。
type BillSummaryLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewBillSummaryLogic 构造账单汇总逻辑.
func NewBillSummaryLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillSummaryLogic {
	return &BillSummaryLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// billSummaryRow 汇总查询的落点.
// 字段名对应 SQL 里的别名(GORM 按列名映射)。
type billSummaryRow struct {
	BillCount    int64           `gorm:"column:bill_count"`
	UnpaidCount  int64           `gorm:"column:unpaid_count"`
	UnpaidAmount decimal.Decimal `gorm:"column:unpaid_amount"`
	PaidCount    int64           `gorm:"column:paid_count"`
	PaidAmount   decimal.Decimal `gorm:"column:paid_amount"`
	TotalAmount  decimal.Decimal `gorm:"column:total_amount"`
}

// BillSummary 汇总指定账期 / 园区的账单金额.
//
// 一次查询出全部 6 个数, 而不是"先 COUNT 未缴再 COUNT 已缴"查好几遍 ——
// 多次查询之间数据可能变化, 算出来的各分项会不自洽(分项之和 ≠ 总额)。
func (l *BillSummaryLogic) BillSummary(req *types.BillSummaryReq) (*types.BillSummaryResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.Period != "" && !validPeriod(req.Period) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "账期格式应为 yyyy-MM")
	}

	// 所有金额列都套 COALESCE: 无匹配行时 SUM 返回 NULL,
	// Scan 进 decimal.Decimal 会直接报错(聚合函数无匹配行返回 NULL 的经典坑)。
	const agg = `
COUNT(*)                                                          AS bill_count,
COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0)          AS unpaid_count,
COALESCE(SUM(CASE WHEN status = 1 THEN amount ELSE 0 END), 0)     AS unpaid_amount,
COALESCE(SUM(CASE WHEN status = 2 THEN 1 ELSE 0 END), 0)          AS paid_count,
COALESCE(SUM(CASE WHEN status = 2 THEN amount ELSE 0 END), 0)     AS paid_amount,
COALESCE(SUM(amount), 0)                                          AS total_amount`

	db := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseBill{})
	if req.Period != "" {
		db = db.Where("billing_period = ?", req.Period)
	}
	// 租户只从 ctx 取(网关注入, 不可伪造), 且**始终过滤** —— 理由同 billlistlogic:
	// 用请求体里的 tenant_id 会让客户端看到别人园区、甚至全部园区的汇总金额。
	db = db.Where("tenant_id = ?", ctxdata.GetTenantId(l.ctx))

	var row billSummaryRow
	if err := db.Select(agg).Scan(&row).Error; err != nil {
		l.Errorf("[lease] summarize bills failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "汇总账单失败")
	}

	return &types.BillSummaryResp{
		Period:       req.Period,
		BillCount:    row.BillCount,
		UnpaidCount:  row.UnpaidCount,
		UnpaidAmount: moneyString(row.UnpaidAmount),
		PaidCount:    row.PaidCount,
		PaidAmount:   moneyString(row.PaidAmount),
		TotalAmount:  moneyString(row.TotalAmount),
	}, nil
}
