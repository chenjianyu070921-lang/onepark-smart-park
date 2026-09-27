package rpcserver

import (
	"context"
	"net"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	alarmpb "onepark/proto/alarm"
	commonpb "onepark/proto/common"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestAlarmServiceOverWire 用真实 gRPC 服务端 + 生成的客户端走一次完整调用.
//
// 交付口径是"对外提供 RPC", 而 server_test.go 是直接调 Go 方法 —— 它覆盖不到
// 服务注册、方法名路由和请求/响应编解码这三件事(例如 proto 改了但 pb 没重新生成,
// 直调用例照样全绿, 线上却是 Unimplemented)。这里补齐这条链路.
func TestAlarmServiceOverWire(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	alarmpb.RegisterAlarmServiceServer(grpcServer, NewAlarmServer(&fakeCountActive{
		total:  9,
		counts: []model.LevelCount{{Level: model.AlarmLevelMajor, Total: 6}, {Level: model.AlarmLevelCritical, Total: 3}},
	}))
	go func() {
		if err := grpcServer.Serve(ln); err != nil {
			t.Logf("serve exited: %v", err)
		}
	}()
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", ln.Addr(), err)
	}
	defer func() { _ = conn.Close() }()

	client := alarmpb.NewAlarmServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Ping(ctx, &commonpb.Empty{}); err != nil {
		t.Fatalf("Ping over wire: %v", err)
	}

	resp, err := client.GetActiveAlarms(ctx, &alarmpb.GetActiveAlarmsReq{
		TenantId: 1,
		AreaId:   12,
		Levels:   []int32{3, 4},
	})
	if err != nil {
		t.Fatalf("GetActiveAlarms over wire: %v", err)
	}
	if resp.GetTotal() != 9 {
		t.Errorf("期望活跃告警总数 9, 实际 %d", resp.GetTotal())
	}
	// M5 大屏把 level_count 拆成四个分项展示并断言"分项之和 == total",
	// 这个恒等式由本服务保证, 必须在契约层守住而不是靠调用方兜.
	var sum int64
	for _, c := range resp.GetLevelCount() {
		sum += c
	}
	if sum != resp.GetTotal() {
		t.Errorf("等级分布之和 %d 与总数 %d 不一致, 大屏分项会加不平", sum, resp.GetTotal())
	}
}

// TestAlarmServiceOverWire_StorageNilDegrade 存储未就绪时线上返回空聚合而非错误,
// 保证 M5 在 alarm-service 无库启动时仍能联调(不因此整体降级为不可用).
func TestAlarmServiceOverWire_StorageNilDegrade(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	alarmpb.RegisterAlarmServiceServer(grpcServer, NewAlarmServer(nil))
	go func() {
		if err := grpcServer.Serve(ln); err != nil {
			t.Logf("serve exited: %v", err)
		}
	}()
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", ln.Addr(), err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := alarmpb.NewAlarmServiceClient(conn).GetActiveAlarms(ctx, &alarmpb.GetActiveAlarmsReq{})
	if err != nil {
		t.Fatalf("存储未就绪不应返回错误: %v", err)
	}
	if resp.GetTotal() != 0 || len(resp.GetLevelCount()) != 0 {
		t.Errorf("期望空聚合, 实际 total=%d levels=%v", resp.GetTotal(), resp.GetLevelCount())
	}
}
