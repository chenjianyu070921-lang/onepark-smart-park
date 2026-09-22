package provider

import (
	"context"

	devicepb "onepark/proto/device"
)

// Device 是基于 M1 device-service gRPC 契约的真实实现.
type Device struct {
	client devicepb.DeviceServiceClient
}

// NewDevice 用 M1 的 gRPC 客户端构造设备数据源.
func NewDevice(client devicepb.DeviceServiceClient) *Device {
	return &Device{client: client}
}

// Stat 取全园区设备台数与状态分布.
//
// 传空请求(type=0): 大屏需要全园区设备总览, type=0 表示"全部类型", 是正确的查询意图.
// M1 device-service 的 GetDeviceStat 已完整支持 type 过滤(1地磁/2门禁/3摄像头...),
// proto 契约与 device 表 type 列均已就绪; 若未来大屏需要按类型分卡片, 传对应 type 即可.
//
// total 直接取 M1 的返回值, 不做本地累加 —— 设备状态的真相源在 M1, M5 不重复计算,
// 否则两边一旦漂移就无法判定谁错。
func (p *Device) Stat(ctx context.Context) (DeviceStat, error) {
	resp, err := p.client.GetDeviceStat(ctx, &devicepb.GetDeviceStatReq{})
	if err != nil {
		return DeviceStat{}, err
	}
	stat := DeviceStat{
		Total:   resp.GetTotal(),
		Online:  resp.GetOnline(),
		Offline: resp.GetOffline(),
		Fault:   resp.GetFault(),
	}
	// 透传 + 日志留痕: 数据不纠正(真相源在 M1), 但不一致必须可见 ——
	// 否则 M1 给 device.status 新增取值后, 大屏会静默少算一截而无人知晓。
	warnIfDrifted("设备", stat.Total, stat.Online+stat.Offline+stat.Fault)
	return stat, nil
}
