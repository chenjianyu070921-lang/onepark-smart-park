package provider

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workorderpb "onepark/proto/workorder"
)

// 本文件补齐 M2 工单适配器的测试 —— 此前 alarm/device/energy 三个适配器都有测试,
// 唯独工单没有(覆盖率 0%)。而它有两个**算出来的值**(完成率、今日新增),
// 算错了大屏只会显示一个错误的数字, 不会有任何报错。

// fakeWorkOrderServer 假工单服务: 按 status 参数返回不同 total, 以便验证"聚合到底查了什么".
type fakeWorkOrderServer struct {
	workorderpb.UnimplementedWorkorderServiceServer
	all     *workorderpb.ListWorkOrdersResp // status=0(不限)时返回
	totals  map[int32]int64                 // status!=0 时返回的 total
	gotReqs []*workorderpb.ListWorkOrdersReq
	err     error
}

func (s *fakeWorkOrderServer) ListWorkOrders(_ context.Context, req *workorderpb.ListWorkOrdersReq) (*workorderpb.ListWorkOrdersResp, error) {
	s.gotReqs = append(s.gotReqs, req)
	if s.err != nil {
		return nil, s.err
	}
	if req.GetStatus() == 0 && s.all != nil {
		return s.all, nil
	}
	return &workorderpb.ListWorkOrdersResp{Total: s.totals[req.GetStatus()]}, nil
}

// newWorkOrderClient 起进程内 gRPC 服务并返回真客户端(与 alarm_test.go 同模式).
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

// TestWorkOrderStat_Mapping 字段映射与两个派生值的算法.
func TestWorkOrderStat_Mapping(t *testing.T) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	fake := &fakeWorkOrderServer{
		all: &workorderpb.ListWorkOrdersResp{
			Total:        100,
			PendingCount: 12, // 待派单 + 处理中
			List: []*workorderpb.WorkOrderSummary{
				{CreatedAt: todayStart},          // 今天
				{CreatedAt: now.Unix()},          // 今天
				{CreatedAt: todayStart - 1},      // 昨天 23:59:59 -> 不算
				{CreatedAt: todayStart - 86400},  // 更早 -> 不算
			},
		},
		totals: map[int32]int64{3: 40, 4: 30}, // 已完成 40 / 已关闭 30
	}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	stat, err := p.Stat(context.Background(), 7)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}

	if stat.TodayTotal != 2 {
		t.Errorf("TodayTotal = %d, 期望 2(只有两条落在今天)", stat.TodayTotal)
	}
	if stat.Unfinished != 12 {
		t.Errorf("Unfinished = %d, 期望 12(直接取 pending_count)", stat.Unfinished)
	}
	// 完成率 = (已完成 40 + 已关闭 30) / 总数 100
	if diff := stat.CompleteRate - 0.70; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CompleteRate = %v, 期望 0.70", stat.CompleteRate)
	}
	// M2 契约未暴露 finished_at -> 平均处理时长不可得, 必须是 nil 而不是 0
	if stat.AvgHandleSec != nil {
		t.Errorf("AvgHandleSec = %v, 期望 nil(M2 契约未暴露 finished_at)", *stat.AvgHandleSec)
	}

	// 契约验证: 租户必须透传, 否则会把别的园区工单算进本园区大屏
	if len(fake.gotReqs) == 0 || fake.gotReqs[0].GetTenantId() != 7 {
		t.Errorf("列表请求未透传 tenant_id, 实际: %+v", fake.gotReqs[0])
	}
	// 列表请求应拉全量(status=0)且用约定的页大小
	if got := fake.gotReqs[0].GetStatus(); got != 0 {
		t.Errorf("全量查询的 status = %d, 期望 0(不限)", got)
	}
}

// TestWorkOrderStat_ZeroTotalNoNaN 总数为 0 时完成率必须是 0, 不能是 NaN.
// (0/0 会得到 NaN, 前端显示会直接炸; 这是除法最容易漏的边界)
func TestWorkOrderStat_ZeroTotalNoNaN(t *testing.T) {
	fake := &fakeWorkOrderServer{
		all:    &workorderpb.ListWorkOrdersResp{Total: 0},
		totals: map[int32]int64{},
	}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if stat.CompleteRate != 0 {
		t.Errorf("CompleteRate = %v, 期望 0(总数为 0 时不能出现 NaN)", stat.CompleteRate)
	}
	if stat.TodayTotal != 0 || stat.Unfinished != 0 {
		t.Errorf("空数据下各计数应为 0, 实际 today=%d unfinished=%d", stat.TodayTotal, stat.Unfinished)
	}
}

// TestWorkOrderStat_ErrorPropagated 上游报错必须向上传 —— 聚合层靠它把该源标成 degraded.
//
// 若这里把错误吞掉返回零值, 大屏会显示"今日工单 0 件"这种**看起来正常但完全错误**的数据,
// 比直接显示"此卡片暂不可用"危险得多。
func TestWorkOrderStat_ErrorPropagated(t *testing.T) {
	fake := &fakeWorkOrderServer{err: errors.New("upstream down")}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	if _, err := p.Stat(context.Background(), 1); err == nil {
		t.Error("上游报错时应向上返回错误, 而不是静默返回零值")
	}
}

// TestCountCreatedToday 纯函数: 今日新增的日期边界.
func TestCountCreatedToday(t *testing.T) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	cases := []struct {
		name string
		list []*workorderpb.WorkOrderSummary
		want int64
	}{
		{"空列表", nil, 0},
		{"今日零点整算今天", []*workorderpb.WorkOrderSummary{{CreatedAt: todayStart}}, 1},
		{"昨日最后一秒不算", []*workorderpb.WorkOrderSummary{{CreatedAt: todayStart - 1}}, 0},
		{"混合只数今天的", []*workorderpb.WorkOrderSummary{
			{CreatedAt: todayStart}, {CreatedAt: todayStart - 1}, {CreatedAt: now.Unix()},
		}, 2},
		{"含 nil 元素不 panic", []*workorderpb.WorkOrderSummary{nil, {CreatedAt: now.Unix()}}, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countCreatedToday(c.list); got != c.want {
				t.Errorf("countCreatedToday = %d, 期望 %d", got, c.want)
			}
		})
	}
}

// TestNotReady_AllReturnErrSourceNotReady 四个未配置占位都必须返回 ErrSourceNotReady.
//
// 这是"降级路径可被真实触发"的依据: 聚合层靠这个哨兵错误把该源列入 degraded,
// 而不是把占位实现当成"数据为 0 的正常源"。
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
