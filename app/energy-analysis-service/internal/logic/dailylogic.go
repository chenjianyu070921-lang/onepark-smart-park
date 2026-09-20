package logic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"onepark/common/errorx"

	"onepark/app/energy-analysis-service/internal/ecode"
	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"
)

// DailyLogic 接口56: 能耗日报
type DailyLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDailyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DailyLogic {
	return &DailyLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DailyLogic) Daily(req *types.DailyRequest) (*types.DailyResponse, error) {
	// 1. 解析日期, 得到"当天 0 点"和"次日 0 点"
	start, end, ok := ParseDay(req.Date)
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "date 格式不对, 应该像 2026-09-15 这样")
	}

	// 2. 按区域汇总这一天的用量
	rows, err := l.svcCtx.EnergyReading.ListZoneUsage(l.ctx, start, end)
	if err != nil {
		return nil, wrapErr("查询日报", err)
	}

	// 3. 先算出全园区总量, 才能算每个区域的占比
	var total float64
	var deviceCount int64
	for _, r := range rows {
		total += r.Usage
		deviceCount += r.DeviceCount
	}

	zones := make([]types.ZoneUsage, 0, len(rows))
	for _, r := range rows {
		zones = append(zones, types.ZoneUsage{
			ZoneId:      r.ZoneID,
			Usage:       round2(r.Usage),
			DeviceCount: r.DeviceCount,
			Percent:     percent(r.Usage, total),
		})
	}

	return &types.DailyResponse{
		Date:        start.Format(timeLayoutDate),
		TotalUsage:  round2(total),
		DeviceCount: deviceCount,
		Zones:       zones,
	}, nil
}
