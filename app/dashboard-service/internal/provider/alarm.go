package provider

import (
	"context"

	alarmpb "onepark/proto/alarm"
)

// 告警等级, 对齐 M3 app/alarm-service/internal/model/alarm.go:
// 1 提示 / 2 一般 / 3 严重 / 4 紧急.
const (
	alarmLevelInfo     int32 = 1
	alarmLevelMinor    int32 = 2
	alarmLevelMajor    int32 = 3
	alarmLevelCritical int32 = 4
)

// Alarm 是基于 M3 alarm-service gRPC 契约的真实实现.
type Alarm struct {
	client alarmpb.AlarmServiceClient
}

// NewAlarm 用 M3 的 gRPC 客户端构造告警数据源.
func NewAlarm(client alarmpb.AlarmServiceClient) *Alarm {
	return &Alarm{client: client}
}

// Stat 取活跃告警总数与等级分布.
//
// M3 已按 level 聚合好返回 map<int32,int64>, M5 不做二次计算,
// 只做"等级码 -> 语义字段"的映射, 保证大屏拿到的四项分之和恒等于 total。
func (p *Alarm) Stat(ctx context.Context, tenantId int64) (AlarmStat, error) {
	resp, err := p.client.GetActiveAlarms(ctx, &alarmpb.GetActiveAlarmsReq{
		TenantId: tenantId,
		// area_id=0 全部区域; levels 为空表示全部等级 —— 大屏要的是全貌
	})
	if err != nil {
		return AlarmStat{}, err
	}

	counts := resp.GetLevelCount()
	return AlarmStat{
		Total:    resp.GetTotal(),
		Critical: counts[alarmLevelCritical],
		Major:    counts[alarmLevelMajor],
		Minor:    counts[alarmLevelMinor],
		Info:     counts[alarmLevelInfo],
	}, nil
}
