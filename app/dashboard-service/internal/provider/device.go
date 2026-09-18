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
// 刻意**只传空请求, 不传 type**: M1 的 device 表当前没有 type 列, 传非 0 会返回
// InvalidArgument(见 proto/device/device.proto 的注释)。待 M1 补该列后再启用按类型过滤。
//
// total 直接取 M1 的返回值, 不做本地累加 —— 设备状态的真相源在 M1, M5 不重复计算,
// 否则两边一旦漂移就无法判定谁错。
func (p *Device) Stat(ctx context.Context) (DeviceStat, error) {
	resp, err := p.client.GetDeviceStat(ctx, &devicepb.GetDeviceStatReq{})
	if err != nil {
		return DeviceStat{}, err
	}
	return DeviceStat{
		Total:   resp.GetTotal(),
		Online:  resp.GetOnline(),
		Offline: resp.GetOffline(),
		Fault:   resp.GetFault(),
	}, nil
}
