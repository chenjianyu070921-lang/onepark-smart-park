package logic

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"onepark/common/errorx"

	"onepark/app/energy-data-service/internal/ecode"
	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"
)

// RealtimeLogic 接口52: 实时设备能耗
type RealtimeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRealtimeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RealtimeLogic {
	return &RealtimeLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *RealtimeLogic) Realtime(req *types.RealtimeRequest) (*types.RealtimeResponse, error) {
	// 1. 参数校验: 没给设备号就不知道查谁
	if req.DeviceId == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "deviceId 不能为空")
	}

	// 2. 查这块表最新的一条读数
	latest, err := l.svcCtx.EnergyReading.FindLatest(l.ctx, req.DeviceId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(ecode.ErrDeviceNoData, "该设备暂无能耗数据")
		}
		logx.WithContext(l.ctx).Errorf("查询实时能耗失败 deviceId=%s err=%v", req.DeviceId, err)
		return nil, errorx.NewError(ecode.ErrQueryFailed, "查询实时能耗失败")
	}

	// 3. 算今天 0 点到现在用了多少度
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrowStart := todayStart.AddDate(0, 0, 1)
	todayUsage := 0.0
	if u, err := l.svcCtx.EnergyReading.UsageBetween(l.ctx, req.DeviceId, todayStart, tomorrowStart); err == nil {
		todayUsage = round2(u)
	}

	// 4. 瞬时功率可能为 NULL, 空的话返回 0
	powerKw := 0.0
	if latest.PowerKw != nil {
		powerKw = *latest.PowerKw
	}

	return &types.RealtimeResponse{
		DeviceId:   latest.DeviceID,
		ZoneId:     latest.ZoneID,
		EnergyKwh:  round2(latest.EnergyKwh),
		PowerKw:    round2(powerKw),
		ReportedAt: latest.ReportedAt.Format(timeLayoutSecond),
		TodayUsage: todayUsage,
	}, nil
}

// round2 保留两位小数, 免得返回 3.0000000000000004 这种数
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
