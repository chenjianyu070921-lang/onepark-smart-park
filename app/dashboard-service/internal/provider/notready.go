package provider

import (
	"context"
	"fmt"
)

// 本文件承载"上游契约尚未就绪 / 未配置上游地址"的数据源占位实现.
// 它们不做硬编码假数据, 而是显式返回 ErrSourceNotReady,
// 由聚合层转成"字段为 null + degraded 列表", 使降级路径可被真实触发与验证.

// Device 占位实现: M1 device-service 目前只有 Ping/SendCommand/GetDevice,
// **没有设备统计接口**(清单 #69/#72), 且团队规范禁止服务间走 HTTP, 故无法绕开。
type Device struct{}

// Stat 始终返回 ErrSourceNotReady.
func (Device) Stat(context.Context) (DeviceStat, error) {
	return DeviceStat{}, fmt.Errorf("%w: M1 device-service 无设备统计接口 (清单 #69/#72)", ErrSourceNotReady)
}

// WorkOrderNotReady 占位实现: 未配置 M2 gRPC 地址时使用.
type WorkOrderNotReady struct{}

// Stat 始终返回 ErrSourceNotReady.
func (WorkOrderNotReady) Stat(context.Context, int64) (WorkOrderStat, error) {
	return WorkOrderStat{}, fmt.Errorf("%w: 未配置 workorder gRPC 地址", ErrSourceNotReady)
}

// AlarmNotReady 占位实现: 未配置 M3 gRPC 地址时使用.
type AlarmNotReady struct{}

// Stat 始终返回 ErrSourceNotReady.
func (AlarmNotReady) Stat(context.Context, int64) (AlarmStat, error) {
	return AlarmStat{}, fmt.Errorf("%w: 未配置 alarm gRPC 地址", ErrSourceNotReady)
}

// EnergyNotReady 占位实现: 未配置 M4 gRPC 地址时使用.
type EnergyNotReady struct{}

// Stat 始终返回 ErrSourceNotReady.
func (EnergyNotReady) Stat(context.Context) (EnergyStat, error) {
	return EnergyStat{}, fmt.Errorf("%w: 未配置 energy gRPC 地址", ErrSourceNotReady)
}
