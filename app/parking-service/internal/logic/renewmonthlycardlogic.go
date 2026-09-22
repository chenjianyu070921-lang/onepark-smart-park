package logic

import (
	"context"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// RenewMonthlyCardLogic 月卡续费逻辑(P2): 延长有效期.
type RenewMonthlyCardLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRenewMonthlyCardLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RenewMonthlyCardLogic {
	return &RenewMonthlyCardLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// RenewMonthlyCard 续费: 把到期时间延长到新 end_time(须晚于当前到期时间, 防止"续费"反而缩短).
// 已停用月卡续费时自动恢复生效(管理语义: 主动续费即视为恢复使用).
func (l *RenewMonthlyCardLogic) RenewMonthlyCard(req *types.RenewMonthlyCardReq) (resp *types.MonthlyCardResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "月卡ID非法")
	}

	now := time.Now()
	newEnd := time.Unix(req.EndTime, 0)
	if !renewEndAfterNow(newEnd, now) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "新到期时间必须晚于当前时间")
	}

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	card, e := fetchMonthlyCard(l.svcCtx.DB, l.ctx, tenantID, req.Id)
	if e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrBadRequest, "月卡不存在")
		}
		l.Errorf("get monthly card failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询月卡失败")
	}
	if !renewEndAfterCur(newEnd, card.EndTime) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "新到期时间必须晚于当前到期时间")
	}

	updates := map[string]interface{}{
		"end_time":   newEnd,
		"status":     model.MonthlyCardStatusActive, // 续费即恢复生效
		"updated_at": time.Now(),
	}
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(card).Updates(updates).Error; e != nil {
		l.Errorf("renew monthly card failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "续费月卡失败")
	}
	card.EndTime = newEnd
	card.Status = model.MonthlyCardStatusActive

	return monthlyCardResp(card), nil
}

// renewEndAfterNow 续费校验(1/2): 新到期时间必须晚于当前时间.
// 抽为纯函数便于单测, 与 RenewMonthlyCard 业务约束保持一致.
func renewEndAfterNow(newEnd, now time.Time) bool {
	return newEnd.After(now)
}

// renewEndAfterCur 续费校验(2/2): 新到期时间必须晚于现有到期时间, 防止"续费"反而缩短有效期.
// 已过期月卡(curEnd 在过去)续费到未来时间即合法, 续费后自动恢复生效(见 RenewMonthlyCard).
func renewEndAfterCur(newEnd, curEnd time.Time) bool {
	return newEnd.After(curEnd)
}
