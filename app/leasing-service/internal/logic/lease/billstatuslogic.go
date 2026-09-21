package lease

import (
	"context"
	"errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// 账单缴费动作.
const (
	billActionPay   = "pay"   // 标记已缴
	billActionUnpay = "unpay" // 撤销缴费
)

// BillStatusLogic 租金账单缴费状态流转(未缴 <-> 已缴).
//
// 为什么需要它: lease_bill.status 早就有"已缴"这个取值, 但此前**没有任何接口能改它** ——
// 账单生成后就是死数据, 收不到款。
type BillStatusLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewBillStatusLogic 构造账单状态逻辑.
func NewBillStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillStatusLogic {
	return &BillStatusLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BillStatus 标记账单已缴 / 撤销缴费.
//
// 幂等设计: 用**条件更新**而不是"先查后改"。
//
//	pay:   UPDATE lease_bill SET status=2 WHERE id=? AND status=1
//	unpay: UPDATE lease_bill SET status=1 WHERE id=? AND status=2
//
// `RowsAffected=0` 有两种可能, 必须分清:
//   - 账单不存在      -> 返回 NotFound
//   - 状态本来就是目标 -> **直接返回成功**(重复点"标记已缴"不该报错)
//
// 之所以不引入 version 乐观锁列: 状态字段本身就是天然的并发守卫,
// 而"已缴->已缴"是幂等操作, 不存在需要检测的冲突。
func (l *BillStatusLogic) BillStatus(req *types.BillStatusReq) (*types.BillStatusResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	var target int8
	switch req.Action {
	case billActionPay:
		target = model.BillStatusPaid
	case billActionUnpay:
		target = model.BillStatusUnpaid
	default:
		return nil, errorx.NewError(errorx.ErrBadRequest, "不支持的操作, 仅支持 pay/unpay")
	}

	// 租户只从 ctx 取(网关注入, 不可伪造): 否则可以跨园区给别人账单缴费/撤销缴费。
	// 账单不存在与"不属于本租户"返回同一个 NotFound, 不泄露"该账单存在"这件事。
	tenantID := ctxdata.GetTenantId(l.ctx)

	var bill model.LeaseBill
	err := l.svcCtx.DB.WithContext(l.ctx).
		Where("id = ? AND tenant_id = ?", req.Id, tenantID).
		First(&bill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(errorx.ErrNotFound, "账单不存在")
	}
	if err != nil {
		l.Errorf("[lease] load bill failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "加载账单失败")
	}

	// 已是目标状态 -> 幂等返回, 不写库
	if bill.Status == target {
		return &types.BillStatusResp{Id: bill.Id, Status: int32(bill.Status)}, nil
	}

	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseBill{}).
		// 条件更新: 状态没被并发改过才生效; 租户条件一并带上, 防跨园区操作
		Where("id = ? AND status = ? AND tenant_id = ?", bill.Id, bill.Status, tenantID).
		Update("status", target)
	if res.Error != nil {
		l.Errorf("[lease] update bill status failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrInternal, "更新账单状态失败")
	}
	if res.RowsAffected == 0 {
		// 期间被别的请求改了: 重新读一次, 把最新状态如实返回, 而不是谎报成功
		var latest model.LeaseBill
		if err := l.svcCtx.DB.WithContext(l.ctx).
			Where("id = ? AND tenant_id = ?", req.Id, tenantID).
			First(&latest).Error; err != nil {
			l.Errorf("[lease] reload bill failed: %v", err)
			return nil, errorx.NewError(errorx.ErrInternal, "读取账单最新状态失败")
		}
		l.Infof("[lease] bill status changed concurrently: billId=%d, now=%d", latest.Id, latest.Status)
		return &types.BillStatusResp{Id: latest.Id, Status: int32(latest.Status)}, nil
	}

	l.Infof("[lease] bill status updated: billId=%d, %d -> %d", bill.Id, bill.Status, target)
	return &types.BillStatusResp{Id: bill.Id, Status: int32(target)}, nil
}
