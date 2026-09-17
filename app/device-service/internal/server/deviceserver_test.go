package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	devicepb "onepark/proto/device"
)

// fakeDeviceModel 只实现 GetDeviceStat 依赖的 CountGroupByStatus,
// 其余方法通过内嵌接口留空(测试路径不会调用).
type fakeDeviceModel struct {
	model.DeviceModel
	counts map[int8]int64
}

func (f *fakeDeviceModel) CountGroupByStatus(_ context.Context, _ string) (map[int8]int64, error) {
	return f.counts, nil
}

func newStatServer(counts map[int8]int64) *DeviceServer {
	return &DeviceServer{
		svcCtx: &svc.ServiceContext{DeviceModel: &fakeDeviceModel{counts: counts}},
	}
}

// TestGetDeviceStat 三分项正确映射且 total 恒等于分项之和.
func TestGetDeviceStat(t *testing.T) {
	s := newStatServer(map[int8]int64{
		model.DeviceStatusOffline: 12,
		model.DeviceStatusOnline:  1240,
		model.DeviceStatusFault:   11,
	})

	resp, err := s.GetDeviceStat(context.Background(), &devicepb.GetDeviceStatReq{})
	if err != nil {
		t.Fatalf("GetDeviceStat 返回错误: %v", err)
	}
	if resp.Online != 1240 || resp.Offline != 12 || resp.Fault != 11 {
		t.Fatalf("分项统计错误: %+v", resp)
	}
	if resp.Total != 1263 {
		t.Fatalf("total 应为 1263, got %d", resp.Total)
	}
	if resp.Total != resp.Online+resp.Offline+resp.Fault {
		t.Fatalf("恒等关系不成立: total=%d, 分项和=%d",
			resp.Total, resp.Online+resp.Offline+resp.Fault)
	}
}

// TestGetDeviceStatEmpty 空表时四个字段全 0, 恒等关系仍成立.
func TestGetDeviceStatEmpty(t *testing.T) {
	s := newStatServer(map[int8]int64{})

	resp, err := s.GetDeviceStat(context.Background(), &devicepb.GetDeviceStatReq{})
	if err != nil {
		t.Fatalf("GetDeviceStat 返回错误: %v", err)
	}
	if resp.Total != 0 || resp.Online != 0 || resp.Offline != 0 || resp.Fault != 0 {
		t.Fatalf("空表统计应全为 0: %+v", resp)
	}
}

// TestGetDeviceStatUnknownStatus 枚举外状态不计入分项, total 仍等于分项之和.
func TestGetDeviceStatUnknownStatus(t *testing.T) {
	s := newStatServer(map[int8]int64{
		model.DeviceStatusOnline: 5,
		99:                       2, // 未知状态
	})

	resp, err := s.GetDeviceStat(context.Background(), &devicepb.GetDeviceStatReq{})
	if err != nil {
		t.Fatalf("GetDeviceStat 返回错误: %v", err)
	}
	if resp.Online != 5 || resp.Offline != 0 || resp.Fault != 0 {
		t.Fatalf("未知状态不应计入分项: %+v", resp)
	}
	if resp.Total != resp.Online+resp.Offline+resp.Fault {
		t.Fatalf("恒等关系不成立: %+v", resp)
	}
}

// TestGetDeviceStatTypeRejected device 表无 type 列, 非 0 类型过滤必须显式拒绝.
func TestGetDeviceStatTypeRejected(t *testing.T) {
	s := newStatServer(nil)

	_, err := s.GetDeviceStat(context.Background(), &devicepb.GetDeviceStatReq{Type: 1})
	if err == nil {
		t.Fatal("type=1 应返回错误, 不能静默忽略过滤条件")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("期望 InvalidArgument, got %v", err)
	}
}
