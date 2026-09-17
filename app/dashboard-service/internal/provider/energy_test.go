package provider

import (
	"context"
	"net"
	"testing"

	energypb "onepark/proto/energy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// 本文件验证 M4 能耗数据源的映射与"缺失指标不伪造"的约定。
// 同样用进程内 gRPC 桩: 真实 M4 库里当下无读数, 返回 0 测不出映射对错。

// fakeEnergyServer 进程内的 M4 gRPC 桩服务.
type fakeEnergyServer struct {
	energypb.UnimplementedEnergyDataServiceServer
	resp   *energypb.GetDailyReportResponse
	gotReq *energypb.GetDailyReportRequest
}

func (s *fakeEnergyServer) GetDailyReport(_ context.Context, req *energypb.GetDailyReportRequest) (*energypb.GetDailyReportResponse, error) {
	s.gotReq = req
	return s.resp, nil
}

// newEnergyClient 通过 bufconn 起一个进程内服务端并返回客户端.
func newEnergyClient(t *testing.T, fake *fakeEnergyServer) energypb.EnergyDataServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	energypb.RegisterEnergyDataServiceServer(srv, fake)
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

	return energypb.NewEnergyDataServiceClient(conn)
}

// TestEnergyStat_KwhMapping 验证当日总用电被正确映射.
func TestEnergyStat_KwhMapping(t *testing.T) {
	fake := &fakeEnergyServer{resp: &energypb.GetDailyReportResponse{
		Date:          "2026-09-16",
		TotalUsageKwh: 12483.5,
	}}
	p := NewEnergy(newEnergyClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if stat.TotalKwh != 12483.5 {
		t.Errorf("TotalKwh = %v, 期望 12483.5", stat.TotalKwh)
	}
}

// TestEnergyStat_WaterAlwaysNil 水耗必须为 nil。
//
// M4 的 GetDailyReportResponse 只有 total_usage_kwh, 没有水耗字段。
// 这里刻意断言 nil 而不是 0: 0 会被大屏读成"园区停水了",
// 与 avg_handle_sec(取不到时返回 null)保持一致的处理原则。
// 若将来 M4 契约补上水耗字段, 这个测试会失败 —— 那时正是提醒我们更新映射的时机。
func TestEnergyStat_WaterAlwaysNil(t *testing.T) {
	fake := &fakeEnergyServer{resp: &energypb.GetDailyReportResponse{TotalUsageKwh: 100}}
	p := NewEnergy(newEnergyClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if stat.TotalWater != nil {
		t.Errorf("TotalWater 应为 nil(M4 契约未提供水耗), 实际: %v", *stat.TotalWater)
	}
}

// TestEnergyStat_EmptyDateMeansToday date 传空表示今天, 由 M4 侧解析 —— 这里确认不外传非法值.
func TestEnergyStat_EmptyDateMeansToday(t *testing.T) {
	fake := &fakeEnergyServer{resp: &energypb.GetDailyReportResponse{}}
	p := NewEnergy(newEnergyClient(t, fake))

	if _, err := p.Stat(context.Background()); err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if fake.gotReq == nil {
		t.Fatal("服务端未收到请求")
	}
	if fake.gotReq.GetDate() != "" || fake.gotReq.GetZoneId() != "" {
		t.Errorf("应查询今天+全园区(空值), 实际 date=%q zone=%q",
			fake.gotReq.GetDate(), fake.gotReq.GetZoneId())
	}
}
