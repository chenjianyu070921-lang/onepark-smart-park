package provider

import (
	"context"
	"fmt"
)

// 本文件承载"上游契约尚未就绪"的数据源占位实现.
// 它们不做硬编码假数据, 而是显式返回 ErrSourceNotReady,
// 由聚合层转成"字段为 null + degraded 列表", 使降级路径可被真实触发与验证.

// AlarmNotReady 占位实现: 未配置 M3 gRPC 地址时使用.
type AlarmNotReady struct{}

// Stat 始终返回 ErrSourceNotReady.
func (AlarmNotReady) Stat(context.Context, int64) (AlarmStat, error) {
	return AlarmStat{}, fmt.Errorf("%w: 未配置 alarm gRPC 地址", ErrSourceNotReady)
}

// Device 占位实现: M1 目前只有 SendCommand/GetDevice, 无设备统计接口(清单 #69/#72).
type Device struct{}

// Stat 始终返回 ErrSourceNotReady.
func (Device) Stat(context.Context) (DeviceStat, error) {
	return DeviceStat{}, fmt.Errorf("%w: M1 device-service 无设备统计接口 (清单 #69/#72)", ErrSourceNotReady)
}

// Energy 占位实现: M4 GetDailyReport 尚未在 proto/energy 中定义(清单 #54).
type Energy struct{}

// Stat 始终返回 ErrSourceNotReady.
func (Energy) Stat(context.Context) (EnergyStat, error) {
	return EnergyStat{}, fmt.Errorf("%w: M4 energy-data-service GetDailyReport (清单 #54) 未定义", ErrSourceNotReady)
}

// WorkOrderNotReady 占位实现: 未配置 M2 gRPC 地址时使用.
type WorkOrderNotReady struct{}

// Stat 始终返回 ErrSourceNotReady.
func (WorkOrderNotReady) Stat(context.Context, int64) (WorkOrderStat, error) {
	return WorkOrderStat{}, fmt.Errorf("%w: 未配置 workorder gRPC 地址", ErrSourceNotReady)
}
