package provider

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workorderpb "onepark/proto/workorder"
)

// 本文件覆盖 M2 工单适配器。
//
// ⚠️ 2026-09-20 合并 develop 时**重写过一次**：M2 在 09-18 联调时把统计字段
// （today_count / pending_count / completion_rate / avg_process_minutes）放进了
// ListWorkOrders 的服务端响应，本端于是改为**单次调用直采**，不再自己拉列表数数 ——
// 因此旧的 `countCreatedToday` 及其日期边界用例一并删除（那个函数已不存在）。
// 这次改动本身是改进：消除了「pageSize=500 截断导致今日新增偏小」与「完成率口径
// 与 M2 不一致」两个漂移，数据归属方（M2）的口径成为唯一口径。
//
// 保留下来的是三类仍然成立的断言：字段映射与**单位换算**、错误必须上传、未配置占位。

// fakeWorkOrderServer 假工单服务。
type fakeWorkOrderServer struct {
	workorderpb.UnimplementedWorkorderServiceServer
	resp    *workorderpb.ListWorkOrdersResp
	gotReqs []*workorderpb.ListWorkOrdersReq
	err     error
}

func (s *fakeWorkOrderServer) ListWorkOrders(_ context.Context, req *workorderpb.ListWorkOrdersReq) (*workorderpb.ListWorkOrdersResp, error) {
	s.gotReqs = append(s.gotReqs, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &workorderpb.ListWorkOrdersResp{}, nil
}

// newWorkOrderClient 起进程内 gRPC 服务并返回真客户端（与 alarm_test.go 同模式）.
func newWorkOrderClient(t *testing.T, fake *fakeWorkOrderServer) workorderpb.WorkorderServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := grpc.NewServer()
	workorderpb.RegisterWorkorderServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return workorderpb.NewWorkorderServiceClient(conn)
}

// TestWorkOrderStat_Mapping 字段直采 + 两个单位换算.
//
// 单位换算是本端唯一的"加工"动作，也是唯一可能算错的地方：
// M2 给「0~100 的百分数」与「分钟」，大屏要「0~1 的比例」与「秒」。
func TestWorkOrderStat_Mapping(t *testing.T) {
	fake := &fakeWorkOrderServer{
		resp: &workorderpb.ListWorkOrdersResp{
			TodayCount:        17,  // M2 按 created_at 当天精确统计
			PendingCount:      12,  // 待派单 + 处理中
			CompletionRate:    70,  // M2 口径: 0~100
			AvgProcessMinutes: 8.5, // M2 口径: 分钟
		},
	}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	stat, err := p.Stat(context.Background(), 7)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}

	if stat.TodayTotal != 17 {
		t.Errorf("TodayTotal = %d, 期望 17(直接采 M2 的 today_count)", stat.TodayTotal)
	}
	if stat.Unfinished != 12 {
		t.Errorf("Unfinished = %d, 期望 12(直接采 pending_count)", stat.Unfinished)
	}
	// 完成率 0~100 -> 0~1
	if diff := stat.CompleteRate - 0.70; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CompleteRate = %v, 期望 0.70(70 / 100)", stat.CompleteRate)
	}
	// 平均处理时长 分钟 -> 秒
	if stat.AvgHandleSec == nil {
		t.Fatal("M2 给了平均处理时长, AvgHandleSec 不应为 nil")
	}
	if *stat.AvgHandleSec != 510 {
		t.Errorf("AvgHandleSec = %v, 期望 510(8.5 分钟 * 60)", *stat.AvgHandleSec)
	}

	// 契约: 租户必须透传, 否则会把别的园区工单算进本园区大屏
	if len(fake.gotReqs) == 0 || fake.gotReqs[0].GetTenantId() != 7 {
		t.Errorf("请求未透传 tenant_id, 实际: %+v", fake.gotReqs[0])
	}
	// 只要聚合字段, 不该拉列表数据
	if got := fake.gotReqs[0].GetPageSize(); got != 1 {
		t.Errorf("PageSize = %d, 期望 1(只消费聚合字段, 不拉列表)", got)
	}
	// 只应调用一次: 旧实现要额外按状态查两次拿完成率, 现在服务端一次给全
	if n := len(fake.gotReqs); n != 1 {
		t.Errorf("上游调用次数 = %d, 期望 1(M2 单次调用即返回全部指标)", n)
	}
}

// TestWorkOrderStat_ZeroValues 空园区的全零响应.
func TestWorkOrderStat_ZeroValues(t *testing.T) {
	fake := &fakeWorkOrderServer{resp: &workorderpb.ListWorkOrdersResp{}}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if stat.CompleteRate != 0 {
		t.Errorf("CompleteRate = %v, 期望 0", stat.CompleteRate)
	}
	if stat.TodayTotal != 0 || stat.Unfinished != 0 {
		t.Errorf("空数据下各计数应为 0, 实际 today=%d unfinished=%d", stat.TodayTotal, stat.Unfinished)
	}
	// 「没有已完成工单所以算不出平均时长」与「平均时长真的是 0 秒」是两件事，
	// 必须是 nil —— 否则大屏会显示「平均处理时长 0 秒」，看起来像性能极好。
	if stat.AvgHandleSec != nil {
		t.Errorf("无已完成工单时 AvgHandleSec 应为 nil, 实际 %v", *stat.AvgHandleSec)
	}
}

// TestWorkOrderStat_ErrorPropagated 上游报错必须向上传 —— 聚合层靠它把该源标成 degraded.
//
// 若这里把错误吞掉返回零值, 大屏会显示「今日工单 0 件」这种**看起来正常但完全错误**的数据,
// 比直接显示「此卡片暂不可用」危险得多。
func TestWorkOrderStat_ErrorPropagated(t *testing.T) {
	fake := &fakeWorkOrderServer{err: errors.New("upstream down")}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	if _, err := p.Stat(context.Background(), 1); err == nil {
		t.Error("上游报错时应向上返回错误, 而不是静默返回零值")
	}
}

// TestNotReady_AllReturnErrSourceNotReady 四个未配置占位都必须返回 ErrSourceNotReady.
//
// 这是「降级路径可被真实触发」的依据: 聚合层靠这个哨兵错误把该源列入 degraded,
// 而不是把占位实现当成「数据为 0 的正常源」。
func TestNotReady_AllReturnErrSourceNotReady(t *testing.T) {
	ctx := context.Background()

	checks := []struct {
		name string
		call func() error
	}{
		{"WorkOrderNotReady", func() error { _, err := WorkOrderNotReady{}.Stat(ctx, 1); return err }},
		{"AlarmNotReady", func() error { _, err := AlarmNotReady{}.Stat(ctx, 1); return err }},
		{"DeviceNotReady", func() error { _, err := DeviceNotReady{}.Stat(ctx); return err }},
		{"EnergyNotReady", func() error { _, err := EnergyNotReady{}.Stat(ctx); return err }},
	}

	for _, c := range checks {
		err := c.call()
		if err == nil {
			t.Errorf("%s 应返回错误", c.name)
			continue
		}
		if !errors.Is(err, ErrSourceNotReady) {
			t.Errorf("%s 的错误应可用 errors.Is 匹配 ErrSourceNotReady, 实际: %v", c.name, err)
		}
	}
}
