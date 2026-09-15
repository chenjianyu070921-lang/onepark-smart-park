// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package lease

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// expiringMaxDays 提醒窗口上限, 防止 days 传成 10086 把整表捞出来.
const expiringMaxDays = 365

// expiringMaxPageSize 单页上限.
const expiringMaxPageSize = 200

type ContractExpiringLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewContractExpiringLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ContractExpiringLogic {
	return &ContractExpiringLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ContractExpiring 到期提醒: 查询未来 N 天内到期(含已逾期仍未处理)的合同.
//
// 为什么条件是 end_date <= today+N 而不是 BETWEEN today AND today+N:
// 已逾期但还没被定时任务转「已到期」的合同是最急的提醒,
// days_left 为负数, 不能因为它不在"未来窗口"内而漏掉。
func (l *ContractExpiringLogic) ContractExpiring(req *types.ContractExpiringReq) (*types.ContractExpiringResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	days := req.Days
	if days <= 0 {
		days = 30
	}
	if days > expiringMaxDays {
		return nil, errorx.NewError(errorx.ErrBadRequest, "days 不能超过 365")
	}
	page, pageSize := clampPage(req.Page, req.PageSize)

	// 截止到第 N 天的当天末尾, 保证"恰好第 N 天到期"的合同也被纳入
	limit := time.Now().AddDate(0, 0, int(days))
	limitEnd := time.Date(limit.Year(), limit.Month(), limit.Day(), 23, 59, 59, 0, time.Local)

	// 用同一组条件做 Count 与 Find, 避免两处条件不一致
	where := func(q *gorm.DB) *gorm.DB {
		return q.Where("status = ?", model.StatusActive).
			Where("end_date <= ?", limitEnd)
	}

	var total int64
	if err := where(l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseContract{})).Count(&total).Error; err != nil {
		l.Errorf("[lease] count expiring contracts failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询到期合同失败")
	}

	var contracts []model.LeaseContract
	if err := where(l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseContract{})).
		Order("end_date ASC"). // 最先到期(或已逾期)的排最前
		Limit(int(pageSize)).Offset(int((page - 1) * pageSize)).
		Find(&contracts).Error; err != nil {
		l.Errorf("[lease] list expiring contracts failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询到期合同失败")
	}

	list := make([]types.ExpiringContract, 0, len(contracts))
	for i := range contracts {
		list = append(list, toExpiringDTO(&contracts[i]))
	}

	return &types.ContractExpiringResp{Total: total, List: list}, nil
}

// toExpiringDTO 模型转 DTO, 顺便算出剩余天数.
func toExpiringDTO(c *model.LeaseContract) types.ExpiringContract {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	endDay := time.Date(c.EndDate.Year(), c.EndDate.Month(), c.EndDate.Day(), 0, 0, 0, 0, time.Local)
	// 只按自然日差计算, 不受当天几点影响
	daysLeft := int64(endDay.Sub(today).Hours() / 24)

	return types.ExpiringContract{
		Id:          c.Id,
		ContractNo:  c.ContractNo,
		TenantId:    c.TenantId,
		TenantName:  c.TenantName,
		ZoneCode:    c.ZoneCode,
		MonthlyRent: c.MonthlyRent.String(),
		EndDate:     c.EndDate.Format(dateLayout),
		DaysLeft:    daysLeft,
		Status:      int32(c.Status),
	}
}

// clampPage 分页参数兜底: page 从 1 起, pageSize 限制在 1~200.
func clampPage(page, pageSize int64) (int64, int64) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > expiringMaxPageSize {
		pageSize = expiringMaxPageSize
	}
	return page, pageSize
}
