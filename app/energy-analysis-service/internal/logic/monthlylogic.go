package logic

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"onepark/common/errorx"

	"onepark/app/energy-analysis-service/internal/ecode"
	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"
)

// MonthlyLogic 接口57: 能耗月报
type MonthlyLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMonthlyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MonthlyLogic {
	return &MonthlyLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *MonthlyLogic) Monthly(req *types.MonthlyRequest) (*types.MonthlyResponse, error) {
	// 1. 解析月份, 得到"当月 1 号 0 点"和"次月 1 号 0 点"
	start, end, ok := ParseMonth(req.Month)
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "month 格式不对, 应该像 2026-09 这样")
	}

	m := l.svcCtx.EnergyReading

	// 2. 本月总用量
	total, err := m.TotalUsage(l.ctx, start, end, "")
	if err != nil {
		return nil, wrapErr("查询月报总用量", err)
	}

	// 3. 每天一条, 给前端画柱状图
	dayRows, err := m.ListDailyUsage(l.ctx, "", start, end)
	if err != nil {
		return nil, wrapErr("查询月报每日明细", err)
	}
	days := make([]types.DayUsage, 0, len(dayRows))
	for _, r := range dayRows {
		days = append(days, types.DayUsage{Date: r.Day, Usage: round2(r.Usage)})
	}

	// 4. 日均用量 = 总用量 / 当月自然天数(不是"有数据的天数", 报表口径统一按自然天)
	daysInMonth := int(end.Sub(start).Hours() / 24)
	avgDaily := 0.0
	if daysInMonth > 0 {
		avgDaily = round2(total / float64(daysInMonth))
	}

	resp := &types.MonthlyResponse{
		Month:         start.Format(timeLayoutMonth),
		TotalUsage:    round2(total),
		AvgDailyUsage: avgDaily,
		Days:          days,
	}

	// 5. 同比: 跟去年同月比
	if lastYear, ok := l.usageIn(start.AddDate(-1, 0, 0), end.AddDate(-1, 0, 0)); ok {
		resp.LastYearUsage = &lastYear
		if lastYear > 0 {
			v := round2((total - lastYear) / lastYear)
			resp.Yoy = &v
		}
	}

	// 6. 环比: 跟上个月比
	if lastMonth, ok := l.usageIn(start.AddDate(0, -1, 0), end.AddDate(0, -1, 0)); ok {
		resp.LastMonthUsage = &lastMonth
		if lastMonth > 0 {
			v := round2((total - lastMonth) / lastMonth)
			resp.Mom = &v
		}
	}

	return resp, nil
}

// usageIn 查某个时间段的用量, 第二个返回值表示"那段时间有没有数据"
// 冷启动没有历史数据时, 同比/环比返回 null, 不能当成 0 来算(否则会算出 -100% 这种假数据)
func (l *MonthlyLogic) usageIn(start, end time.Time) (float64, bool) {
	n, err := l.svcCtx.EnergyReading.CountDevice(l.ctx, start, end, "")
	if err != nil || n == 0 {
		return 0, false
	}
	usage, err := l.svcCtx.EnergyReading.TotalUsage(l.ctx, start, end, "")
	if err != nil {
		return 0, false
	}
	return usage, true
}
