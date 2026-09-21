package lease

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// BillListLogic 租金账单分页列表.
//
// 为什么需要它: 定时任务每月把账单生成出来, 但此前**没有任何查询接口** ——
// 账单生成了却看不到, 等于白生成。
type BillListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewBillListLogic 构造账单列表逻辑.
func NewBillListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillListLogic {
	return &BillListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BillList 按合同 / 账期 / 缴费状态 / 园区分页查询账单.
func (l *BillListLogic) BillList(req *types.BillListReq) (*types.BillListResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > maxPageSize {
		req.PageSize = 10
	}
	// 账期格式非法直接报错, 而不是静默返回空列表 ——
	// 静默会让调用方以为"这段账期没有账单", 把参数写错当成数据为空。
	if req.Period != "" && !validPeriod(req.Period) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "账期格式应为 yyyy-MM")
	}

	// 用闭包构造查询条件, 避免 Count 与 Find 共用同一个被 GORM 改写的 Statement.
	scope := func() *gorm.DB {
		db := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseBill{})
		if req.ContractId != 0 {
			db = db.Where("contract_id = ?", req.ContractId)
		}
		if req.Period != "" {
			db = db.Where("billing_period = ?", req.Period)
		}
		if req.Status != 0 {
			db = db.Where("status = ?", req.Status)
		}
		// 租户只从 ctx 取(网关注入, 不可伪造), 且**始终过滤**。
		// ⚠️ 不能用请求体里的 tenant_id: 否则客户端传别人园区 id 就能看别人的账单,
		// 传 0 更会直接看到全部园区。与 contractlistlogic 的口径保持一致。
		db = db.Where("tenant_id = ?", ctxdata.GetTenantId(l.ctx))
		return db
	}

	var total int64
	if err := scope().Count(&total).Error; err != nil {
		l.Errorf("[lease] count bills failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询账单列表失败")
	}

	records := make([]model.LeaseBill, 0, req.PageSize)
	if err := scope().
		// 账期倒序(最近账期在前); 同账期按合同ID升序。
		// (billing_period, contract_id) 上有唯一索引 uk_contract_period,
		// 因此排序键唯一 -> 分页不会出现"同一行在两页里重复出现"。
		Order("billing_period DESC, contract_id ASC").
		Offset(int((req.Page - 1) * req.PageSize)).
		Limit(int(req.PageSize)).
		Find(&records).Error; err != nil {
		l.Errorf("[lease] page bills failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询账单列表失败")
	}

	items := make([]types.Bill, 0, len(records))
	for i := range records {
		items = append(items, toBillDTO(&records[i]))
	}

	return &types.BillListResp{Total: total, List: items}, nil
}
