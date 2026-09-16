package provider

import (
	"context"

	alarmpb "onepark/proto/alarm"
)

// 告警等级码, 对应 M3 alarm-service 的等级定义(1 致命 / 2 严重 / 3 一般 / 4 提示):
const (
	alarmLevelCritical = 1
	alarmLevelMajor    = 2
	alarmLevelMinor    = 3
)

// Alarm 是基于 M3 alarm-service gRPC 契约(GetActiveAlarms, 清单 #43)的真实实现.
type Alarm struct {
	client alarmpb.AlarmServiceClient
}

// NewAlarm 由 M3 的 gRPC 客户端构造告警数据源.
func NewAlarm(client alarmpb.AlarmServiceClient) *Alarm {
	return &Alarm{client: client}
}

// Stat 聚合大屏告警卡片的指标, 全部来自 M3 gRPC 的真实返回值:
//
//   - Total    GetActiveAlarmsResp.total(活跃告警总数)
//   - Critical level_count[1](致命)
//   - Major    level_count[2](严重)
//   - Minor    level_count[3]+level_count[4](一般+提示)
func (p *Alarm) Stat(ctx context.Context, tenantId int64) (AlarmStat, error) {
	resp, err := p.client.GetActiveAlarms(ctx, &alarmpb.GetActiveAlarmsReq{
		TenantId: tenantId,
	})
	if err != nil {
		return AlarmStat{}, err
	}

	stat := AlarmStat{Total: resp.GetTotal()}
	for level, count := range resp.GetLevelCount() {
		switch level {
		case alarmLevelCritical:
			stat.Critical = count
		case alarmLevelMajor:
			stat.Major = count
		case alarmLevelMinor, 4:
			stat.Minor += count
		}
	}
	return stat, nil
}
