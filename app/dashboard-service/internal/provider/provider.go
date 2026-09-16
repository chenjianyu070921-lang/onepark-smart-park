// Package provider 定义 dashboard 聚合所需的外部数据源端口(依赖倒置).
//
// 按 docs/服务协议规范-HTTP与gRPC.md: 服务间调用一律走 gRPC Client,
// 不使用 http.Client 调别的服务 HTTP 接口; 客户端在 servicecontext 中注入.
//
// 上游契约未就绪的数据源返回 ErrSourceNotReady, 由聚合层统一转成
// "该字段为 null + degraded 标记", 保证接口整体不返回 5xx.
package provider

import (
	"context"
	"errors"
)

// ErrSourceNotReady 表示上游服务的契约或实现尚未就绪.
var ErrSourceNotReady = errors.New("data source not ready")

// WorkOrderStat 工单统计, 数据源: M2 workorder-service ListWorkOrders.
type WorkOrderStat struct {
	TodayTotal   int64
	Unfinished   int64
	AvgHandleSec *float64 // 契约未暴露 finished_at, 该指标暂不可得时为 nil
	CompleteRate float64
}

// AlarmStat 告警统计, 数据源: M3 alarm-service GetActiveAlarms(清单 #43).
type AlarmStat struct {
	Total    int64
	Critical int64
	Major    int64
	Minor    int64
}

// DeviceStat 设备统计, 数据源: M1 device-service(清单 #69 / #72).
type DeviceStat struct {
	Total   int64
	Online  int64
	Offline int64
}

// EnergyStat 今日能耗, 数据源: M4 energy-data-service GetDailyReport(清单 #54).
type EnergyStat struct {
	TotalKwh   float64
	TotalWater float64
}

// WorkOrderProvider 工单数据源端口.
type WorkOrderProvider interface {
	Stat(ctx context.Context, tenantId int64) (WorkOrderStat, error)
}

// AlarmProvider 告警数据源端口.
// tenantId 为 0 表示不过滤(系统级视图); 非 0 时按租户隔离聚合.
type AlarmProvider interface {
	Stat(ctx context.Context, tenantId int64) (AlarmStat, error)
}

// DeviceProvider 设备数据源端口.
type DeviceProvider interface {
	Stat(ctx context.Context) (DeviceStat, error)
}

// EnergyProvider 能耗数据源端口.
type EnergyProvider interface {
	Stat(ctx context.Context) (EnergyStat, error)
}
