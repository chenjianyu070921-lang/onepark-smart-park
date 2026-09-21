package logic

import (
	"context"
	"testing"
	"time"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
)

// fakeListDeviceModel 只实现 DeviceList 依赖的 FindList,
// 其余方法通过内嵌接口留空(测试路径不会调用).
type fakeListDeviceModel struct {
	model.DeviceModel
	list       []*model.Device
	total      int64
	gotPage    int
	gotSize    int
	gotProduct string
	gotStatus  int8
}

func (f *fakeListDeviceModel) FindList(_ context.Context, page, size int, productKey string, status int8) ([]*model.Device, int64, error) {
	f.gotPage, f.gotSize, f.gotProduct, f.gotStatus = page, size, productKey, status
	return f.list, f.total, nil
}

// TestDeviceList 列表结果应正确映射为响应项, 且分页/过滤参数透传到模型层.
func TestDeviceList(t *testing.T) {
	now := time.Now()
	fake := &fakeListDeviceModel{
		list: []*model.Device{
			{
				DeviceID:   "dev-001",
				DeviceName: "1号地磁",
				ProductKey: "pk_parking_geo",
				Status:     model.DeviceStatusOnline,
				Location:   "A区入口",
				CreatedAt:  now,
			},
		},
		total: 1,
	}
	l := NewDeviceListLogic(context.Background(), &svc.ServiceContext{DeviceModel: fake})

	resp, err := l.DeviceList(&types.DeviceListReq{Page: 2, Size: 5, ProductKey: "pk_parking_geo", Status: 1})
	if err != nil {
		t.Fatalf("DeviceList 返回错误: %v", err)
	}
	if fake.gotPage != 2 || fake.gotSize != 5 || fake.gotProduct != "pk_parking_geo" || fake.gotStatus != 1 {
		t.Fatalf("查询参数未透传: page=%d size=%d product=%q status=%d",
			fake.gotPage, fake.gotSize, fake.gotProduct, fake.gotStatus)
	}
	if resp.Total != 1 || len(resp.List) != 1 {
		t.Fatalf("列表数量错误: total=%d len=%d", resp.Total, len(resp.List))
	}
	item := resp.List[0]
	if item.DeviceID != "dev-001" || item.DeviceName != "1号地磁" ||
		item.ProductKey != "pk_parking_geo" || item.Status != model.DeviceStatusOnline ||
		item.Location != "A区入口" {
		t.Fatalf("设备项映射错误: %+v", item)
	}
	if item.CreatedAt != now.Format(time.DateTime) {
		t.Fatalf("创建时间格式化错误: got %q, want %q", item.CreatedAt, now.Format(time.DateTime))
	}
}
