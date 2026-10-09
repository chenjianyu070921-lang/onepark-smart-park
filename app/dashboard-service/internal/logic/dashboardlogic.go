package logic

import (
	"context"
	"time"

	energypb "onepark/proto/energy"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DashboardLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDashboardLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DashboardLogic {
	return &DashboardLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DashboardLogic) Dashboard(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}

// EnergyCard 大屏能耗卡片: 调 M4 energy-data-service 接口54(GetDailyReport)
func (l *DashboardLogic) EnergyCard(req *types.EnergyCardReq) (*types.EnergyCardResp, error) {
	ctx, cancel := context.WithTimeout(l.ctx, 5*time.Second)
	defer cancel()

	resp, err := l.svcCtx.Energy.GetDailyReport(ctx, &energypb.GetDailyReportRequest{
		Date:   req.Date,
		ZoneId: req.ZoneId,
	})
	if err != nil {
		l.Errorf("GetDailyReport(%s, %s) 失败: %v", req.Date, req.ZoneId, err)
		return nil, err
	}

	return &types.EnergyCardResp{
		Date:          resp.GetDate(),
		ZoneId:        resp.GetZoneId(),
		TotalUsageKwh: resp.GetTotalUsageKwh(),
		UpdatedAt:     resp.GetUpdatedAt(),
	}, nil
}
