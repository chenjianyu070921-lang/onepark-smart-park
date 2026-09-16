package provider

import (
	"context"
	"net"
	"testing"

	alarmpb "onepark/proto/alarm"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// 本文件验证 M3 告警数据源的**映射逻辑**, 用一个进程内的真 gRPC 服务端返回预设值。
//
// 为什么不直接连 M3 的真实服务: 那边库当下没有告警数据, 返回全 0,
// 恰好证明不了"等级码 -> 语义字段"的映射是否正确 —— 而映射错一格,
// 大屏就会把"提示"显示成"紧急"。用确定性输入才测得出对错。

// fakeAlarmServer 进程内的 M3 gRPC 桩服务.
type fakeAlarmServer struct {
	alarmpb.UnimplementedAlarmServiceServer
	resp   *alarmpb.GetActiveAlarmsResp
	gotReq *alarmpb.GetActiveAlarmsReq
}

func (s *fakeAlarmServer) GetActiveAlarms(_ context.Context, req *alarmpb.GetActiveAlarmsReq) (*alarmpb.GetActiveAlarmsResp, error) {
	s.gotReq = req
	return s.resp, nil
}

// newAlarmClient 通过 bufconn 起一个进程内服务端并返回客户端.
func newAlarmClient(t *testing.T, fake *fakeAlarmServer) alarmpb.AlarmServiceClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	alarmpb.RegisterAlarmServiceServer(srv, fake)
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

	return alarmpb.NewAlarmServiceClient(conn)
}

// TestAlarmStat_LevelMapping 验证 M3 的 level_count 被正确映射到四个语义字段。
// 等级口径来自 M3 app/alarm-service/internal/model/alarm.go:
// 1 提示 / 2 一般 / 3 严重 / 4 紧急。
func TestAlarmStat_LevelMapping(t *testing.T) {
	fake := &fakeAlarmServer{resp: &alarmpb.GetActiveAlarmsResp{
		Total: 12,
		LevelCount: map[int32]int64{
			1: 5, // 提示 -> Info
			2: 4, // 一般 -> Minor
			3: 2, // 严重 -> Major
			4: 1, // 紧急 -> Critical
		},
	}}
	p := NewAlarm(newAlarmClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}

	if stat.Critical != 1 {
		t.Errorf("Critical = %d, 期望 1 (level=4 紧急)", stat.Critical)
	}
	if stat.Major != 2 {
		t.Errorf("Major = %d, 期望 2 (level=3 严重)", stat.Major)
	}
	if stat.Minor != 4 {
		t.Errorf("Minor = %d, 期望 4 (level=2 一般)", stat.Minor)
	}
	if stat.Info != 5 {
		t.Errorf("Info = %d, 期望 5 (level=1 提示)", stat.Info)
	}
	if stat.Total != 12 {
		t.Errorf("Total = %d, 期望 12", stat.Total)
	}

	// 四项分项之和必须等于 total —— 否则大屏上"总数和分组对不上"
	if sum := stat.Critical + stat.Major + stat.Minor + stat.Info; sum != stat.Total {
		t.Errorf("分项之和 %d != Total %d", sum, stat.Total)
	}
}

// TestAlarmStat_UnknownLevelIgnored 未在口径内的等级码不污染任何语义字段.
func TestAlarmStat_UnknownLevelIgnored(t *testing.T) {
	fake := &fakeAlarmServer{resp: &alarmpb.GetActiveAlarmsResp{
		Total:      10,
		LevelCount: map[int32]int64{1: 3, 9: 7}, // 9 是未知等级
	}}
	p := NewAlarm(newAlarmClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if stat.Info != 3 {
		t.Errorf("Info = %d, 期望 3", stat.Info)
	}
	if stat.Critical+stat.Major+stat.Minor != 0 {
		t.Errorf("未知等级不应被映射到语义字段: %+v", stat)
	}
}

// TestAlarmStat_TenantPassedThrough 园区ID必须透传给 M3。
// 漏传(或恒传 0)会把别的园区的告警显示到本园区大屏上。
func TestAlarmStat_TenantPassedThrough(t *testing.T) {
	fake := &fakeAlarmServer{resp: &alarmpb.GetActiveAlarmsResp{}}
	p := NewAlarm(newAlarmClient(t, fake))

	if _, err := p.Stat(context.Background(), 42); err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if fake.gotReq == nil {
		t.Fatal("服务端未收到请求")
	}
	if fake.gotReq.GetTenantId() != 42 {
		t.Errorf("透传的 tenant_id = %d, 期望 42", fake.gotReq.GetTenantId())
	}
}

// TestAlarmStat_EmptyResponse 无活跃告警时四个字段都应是 0, 而不是报错.
func TestAlarmStat_EmptyResponse(t *testing.T) {
	fake := &fakeAlarmServer{resp: &alarmpb.GetActiveAlarmsResp{}}
	p := NewAlarm(newAlarmClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}
	if stat.Total != 0 || stat.Critical != 0 || stat.Major != 0 || stat.Minor != 0 || stat.Info != 0 {
		t.Errorf("空响应应全部为 0: %+v", stat)
	}
}
