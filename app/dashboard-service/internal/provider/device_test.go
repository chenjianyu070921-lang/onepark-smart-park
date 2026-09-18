package provider

import (
	"context"
	"net"
	"testing"

	devicepb "onepark/proto/device"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// 本文件验证 M1 设备数据源的映射与调用约定, 用进程内真 gRPC 服务端返回预设值。

// fakeDeviceServer 进程内的 M1 gRPC 桩服务.
type fakeDeviceServer struct {
	devicepb.UnimplementedDeviceServiceServer
	resp   *devicepb.GetDeviceStatResp
	gotReq *devicepb.GetDeviceStatReq
}

func (s *fakeDeviceServer) GetDeviceStat(_ context.Context, req *devicepb.GetDeviceStatReq) (*devicepb.GetDeviceStatResp, error) {
	s.gotReq = req
	return s.resp, nil
}

// newDeviceClient 通过 bufconn 起一个进程内服务端并返回客户端.
func newDeviceClient(t *testing.T, fake *fakeDeviceServer) devicepb.DeviceServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	devicepb.RegisterDeviceServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("建立 gRPC 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return devicepb.NewDeviceServiceClient(conn)
}

// TestDeviceStat_StatusMapping 验证四个字段的映射, 并锁定不变量:
//
//	total == online + offline + fault
//
// device.status 有三个取值(0离线/1在线/2故障), 若 M5 只暴露两项, 大屏就会出现
// "总 1263 台、在线 1240、离线 12"(相加只有 1252)—— 这正是本用例要防住的。
func TestDeviceStat_StatusMapping(t *testing.T) {
	fake := &fakeDeviceServer{resp: &devicepb.GetDeviceStatResp{
		Total:   1263,
		Online:  1240,
		Offline: 12,
		Fault:   11,
	}}
	p := NewDevice(newDeviceClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}

	if stat.Total != 1263 {
		t.Errorf("Total = %d, 期望 1263", stat.Total)
	}
	if stat.Online != 1240 {
		t.Errorf("Online = %d, 期望 1240", stat.Online)
	}
	if stat.Offline != 12 {
		t.Errorf("Offline = %d, 期望 12", stat.Offline)
	}
	if stat.Fault != 11 {
		t.Errorf("Fault = %d, 期望 11", stat.Fault)
	}

	// 不变量: 三项分之和必须等于总数。
	// 若将来 M1 给 device.status 增加新取值而契约未同步, 本断言会失败 —— 这正是它存在的意义。
	if sum := stat.Online + stat.Offline + stat.Fault; sum != stat.Total {
		t.Errorf("分项之和 %d != Total %d —— 契约可能新增了状态码, 需双方同步", sum, stat.Total)
	}
}

// TestDeviceStat_NoTypeFilter 必须只发空请求。
//
// M1 的 device 表当前没有 type 列, 传非 0 会返回 InvalidArgument
// (见 proto/device/device.proto 的注释)。本用例把这个约定钉住,
// 防止日后有人"顺手"加上类型过滤导致设备卡片整路报错。
func TestDeviceStat_NoTypeFilter(t *testing.T) {
	fake := &fakeDeviceServer{resp: &devicepb.GetDeviceStatResp{}}
	p := NewDevice(newDeviceClient(t, fake))

	if _, err := p.Stat(context.Background()); err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if fake.gotReq == nil {
		t.Fatal("服务端未收到请求")
	}
	if fake.gotReq.GetType() != 0 {
		t.Errorf("type = %d, 期望 0(M1 device 表无 type 列, 传非 0 会报 InvalidArgument)", fake.gotReq.GetType())
	}
	if fake.gotReq.GetProductKey() != "" {
		t.Errorf("product_key = %q, 期望空(统计全园区全部产品)", fake.gotReq.GetProductKey())
	}
}

// TestDeviceStat_PassThroughOnInconsistency 上游数字自相矛盾时, M5 **原样透传, 不做本地纠正**。
//
// 理由: 设备状态的真相源在 M1, M5 若擅自补齐差额就会掩盖上游 bug,
// 让问题"看起来已经好了"。透传 + 日志留痕, 才能把责任与线索都留在正确的地方。
func TestDeviceStat_PassThroughOnInconsistency(t *testing.T) {
	fake := &fakeDeviceServer{resp: &devicepb.GetDeviceStatResp{
		Total:   100,
		Online:  90,
		Offline: 1, // 90 + 1 = 91 != 100, 上游自相矛盾
		Fault:   0,
	}}
	p := NewDevice(newDeviceClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应因上游数字不一致而报错(整体可用性优先): %v", err)
	}
	if stat.Total != 100 || stat.Online != 90 || stat.Offline != 1 || stat.Fault != 0 {
		t.Errorf("应原样透传上游数值, 不做本地纠正: %+v", stat)
	}
}

// TestDeviceStat_ZeroDevices 无设备时四个字段都应是 0, 而不是报错.
func TestDeviceStat_ZeroDevices(t *testing.T) {
	fake := &fakeDeviceServer{resp: &devicepb.GetDeviceStatResp{}}
	p := NewDevice(newDeviceClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if stat.Total != 0 || stat.Online != 0 || stat.Offline != 0 || stat.Fault != 0 {
		t.Errorf("无设备时应全部为 0: %+v", stat)
	}
}
