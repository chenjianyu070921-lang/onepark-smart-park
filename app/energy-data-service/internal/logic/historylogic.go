package logic

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"onepark/common/errorx"

	"onepark/app/energy-data-service/internal/ecode"
	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"
)

// HistoryLogic 接口53: 历史能耗曲线
type HistoryLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewHistoryLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HistoryLogic {
	return &HistoryLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *HistoryLogic) History(req *types.HistoryRequest) (*types.HistoryResponse, error) {
	// 1. 参数校验
	if req.DeviceId == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "deviceId 不能为空")
	}

	// 2. 时间范围: 没传就默认今天一整天
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start, ok := parseTime(req.Start, todayStart)
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "start 时间格式不对, 例: 2026-09-15")
	}
	end, ok := parseTime(req.End, todayStart.AddDate(0, 0, 1))
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "end 时间格式不对, 例: 2026-09-15")
	}
	if !end.After(start) {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "end 必须晚于 start")
	}

	// 3. 粒度: hour 按小时, day 按天, 默认小时
	layout := mysqlLayoutHour
	if req.Granularity == "day" {
		layout = mysqlLayoutDay
	}

	// 4. 按粒度查每个时间段的用量
	points, err := l.svcCtx.EnergyReading.ListUsage(l.ctx, req.DeviceId, start, end, layout)
	if err != nil {
		logx.WithContext(l.ctx).Errorf("查询历史曲线失败 deviceId=%s err=%v", req.DeviceId, err)
		return nil, errorx.NewError(ecode.ErrQueryFailed, "查询历史曲线失败")
	}

	// 5. 总用量 = 期末读数 - 期初读数(不是把每段加起来, 那样会漏掉跨段的差值)
	totalUsage := 0.0
	if u, err := l.svcCtx.EnergyReading.UsageBetween(l.ctx, req.DeviceId, start, end); err == nil {
		totalUsage = round2(u)
	}

	resp := &types.HistoryResponse{
		DeviceId:   req.DeviceId,
		Points:     make([]types.HistoryPoint, 0, len(points)),
		TotalUsage: totalUsage,
	}
	for _, p := range points {
		resp.Points = append(resp.Points, types.HistoryPoint{
			Time:  p.Bucket,
			Usage: round2(p.Usage),
		})
	}
	return resp, nil
}
