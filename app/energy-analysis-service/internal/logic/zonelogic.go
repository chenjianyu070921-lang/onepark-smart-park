package logic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"onepark/common/errorx"

	"onepark/app/energy-analysis-service/internal/ecode"
	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"
)

// ZoneLogic 接口58: 指定区域能耗详情
type ZoneLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewZoneLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ZoneLogic {
	return &ZoneLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ZoneLogic) Zone(req *types.ZoneDetailRequest) (*types.ZoneDetailResponse, error) {
	// 1. 参数校验: 没给区域就不知道查哪
	if req.ZoneId == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "zoneId 不能为空")
	}

	// 2. 解析时间范围, 不传就是今天一整天
	start, end, ok := ParseRange(req.Start, req.End)
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "时间范围不合法, start 要早于 end, 格式像 2026-09-15")
	}

	m := l.svcCtx.EnergyReading

	// 3. 该区域每台设备各用了多少
	deviceRows, err := m.ListDeviceUsage(l.ctx, req.ZoneId, start, end)
	if err != nil {
		return nil, wrapErr("查询区域设备用量", err)
	}
	if len(deviceRows) == 0 {
		return nil, errorx.NewError(ecode.ErrZoneNoData, "该区域在指定时间范围内没有能耗数据")
	}

	// 4. 每台设备最后一次的读数(展示"电表跑到现在总共多少度")
	lastRows, err := m.ListLastReading(l.ctx, req.ZoneId, start, end)
	if err != nil {
		return nil, wrapErr("查询设备最新读数", err)
	}
	lastMap := make(map[string]struct {
		kwh float64
		at  string
	}, len(lastRows))
	for _, r := range lastRows {
		lastMap[r.DeviceID] = struct {
			kwh float64
			at  string
		}{kwh: round2(r.EnergyKwh), at: r.ReportedAt.Format(timeLayoutSecond)}
	}

	// 5. 拼设备明细, 顺便算出区域总量和各自占比
	var total float64
	for _, r := range deviceRows {
		total += r.Usage
	}

	devices := make([]types.DeviceUsage, 0, len(deviceRows))
	for _, r := range deviceRows {
		last := lastMap[r.DeviceID]
		devices = append(devices, types.DeviceUsage{
			DeviceId:       r.DeviceID,
			Usage:          round2(r.Usage),
			Percent:        percent(r.Usage, total),
			LastReading:    last.kwh,
			LastReportedAt: last.at,
		})
	}

	// 6. 按天趋势, 给前端画柱状图
	dayRows, err := m.ListDailyUsage(l.ctx, req.ZoneId, start, end)
	if err != nil {
		return nil, wrapErr("查询区域按天趋势", err)
	}
	days := make([]types.DayUsage, 0, len(dayRows))
	for _, r := range dayRows {
		days = append(days, types.DayUsage{Date: r.Day, Usage: round2(r.Usage)})
	}

	count, err := m.CountDevice(l.ctx, start, end, req.ZoneId)
	if err != nil {
		return nil, wrapErr("统计区域设备数", err)
	}

	return &types.ZoneDetailResponse{
		ZoneId:      req.ZoneId,
		Start:       start.Format(timeLayoutSecond),
		End:         end.Format(timeLayoutSecond),
		TotalUsage:  round2(total),
		DeviceCount: count,
		Devices:     devices,
		Days:        days,
	}, nil
}
