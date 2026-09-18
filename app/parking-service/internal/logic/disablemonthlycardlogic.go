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

// DisableMonthlyCardLogic 月卡停用逻辑(P2).
type DisableMonthlyCardLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDisableMonthlyCardLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DisableMonthlyCardLogic {
	return &DisableMonthlyCardLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// DisableMonthlyCard 停用月卡: 置 status=停用, 停用后入场不再按月卡识别(按临时车计费).
// 幂等: 已停用的卡重复停用直接返回成功(管理操作幂等更友好).
func (l *DisableMonthlyCardLogic) DisableMonthlyCard(req *types.MonthlyCardIdReq) (resp *types.MonthlyCardResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "月卡ID非法")
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

	if card.Status != model.MonthlyCardStatusDisabled {
		if e := l.svcCtx.DB.WithContext(l.ctx).Model(card).
			Updates(map[string]interface{}{
				"status":     model.MonthlyCardStatusDisabled,
				"updated_at": time.Now(),
			}).Error; e != nil {
			l.Errorf("disable monthly card failed: %v", e)
			return nil, errorx.NewError(errorx.ErrM2Internal, "停用月卡失败")
		}
		card.Status = model.MonthlyCardStatusDisabled
	}

	return monthlyCardResp(card), nil
}
