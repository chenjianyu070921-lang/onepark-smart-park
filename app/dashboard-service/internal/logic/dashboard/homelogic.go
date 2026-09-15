package dashboard

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
)

// HomeLogic 首页概览: 当前只接已就绪的 M2 工单数据源.
type HomeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewHomeLogic 构造首页概览逻辑.
func NewHomeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HomeLogic {
	return &HomeLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Home 返回首页工单卡片.
// 数据源不可用时返回 available=false 的卡片, 接口本身仍为 200.
func (l *HomeLogic) Home(req *types.HomeReq) (*types.HomeResp, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(l.ctx, sourceTimeout)
	defer cancel()

	card := types.WorkOrderCard{}
	stat, err := l.svcCtx.Providers.WorkOrder.Stat(ctx, req.TenantId)
	if err != nil {
		l.Errorf("[home] work_order source failed: %v", err)
		card.Available = false
	} else {
		card.Available = true
		card.TodayTotal = stat.TodayTotal
		card.Unfinished = stat.Unfinished
		card.AvgHandleSec = stat.AvgHandleSec
		card.CompleteRate = stat.CompleteRate
	}

	return &types.HomeResp{
		WorkOrder: card,
		UpdatedAt: time.Now().Unix(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}, nil
}
