package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/dashboard-service/internal/provider"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"onepark/common/redisx"
)

// 本文件验证 Overview 聚合逻辑的三个核心承诺:
//  1. 四路数据源并行聚合, 全部成功时字段完整
//  2. 任意一路失败/超时 -> 该字段为 null + degraded 标记, 整体不报错(组长验收口径)
//  3. 缓存命中时不再穿透到数据源
//
// 数据源用测试桩替代真实 gRPC —— 这正是 provider 端口做依赖倒置的意义:
// 不需要 M1/M2/M3/M4 真的在线, 就能验证聚合/降级/缓存全部路径。

// ---------- 测试桩: 可配置返回值 / 错误 / 阻塞时长 ----------

type stubWorkOrder struct {
	stat  provider.WorkOrderStat
	err   error
	block time.Duration
}

func (s *stubWorkOrder) Stat(ctx context.Context, _ int64) (provider.WorkOrderStat, error) {
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return provider.WorkOrderStat{}, ctx.Err()
		}
	}
	return s.stat, s.err
}

type stubAlarm struct {
	stat  provider.AlarmStat
	err   error
	block time.Duration
}

func (s *stubAlarm) Stat(ctx context.Context) (provider.AlarmStat, error) {
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return provider.AlarmStat{}, ctx.Err()
		}
	}
	return s.stat, s.err
}

type stubDevice struct {
	stat  provider.DeviceStat
	err   error
	block time.Duration
}

func (s *stubDevice) Stat(ctx context.Context) (provider.DeviceStat, error) {
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return provider.DeviceStat{}, ctx.Err()
		}
	}
	return s.stat, s.err
}

type stubEnergy struct {
	stat  provider.EnergyStat
	err   error
	block time.Duration
}

func (s *stubEnergy) Stat(ctx context.Context) (provider.EnergyStat, error) {
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return provider.EnergyStat{}, ctx.Err()
		}
	}
	return s.stat, s.err
}

// okStubs 四路全部正常的桩.
func okStubs() (*stubWorkOrder, *stubAlarm, *stubDevice, *stubEnergy) {
	avg := 3600.5
	return &stubWorkOrder{stat: provider.WorkOrderStat{
			TodayTotal:   12,
			Unfinished:   7,
			AvgHandleSec: &avg,
			CompleteRate: 0.75,
		}},
		&stubAlarm{stat: provider.AlarmStat{Total: 3, Critical: 1, Major: 2}},
		&stubDevice{stat: provider.DeviceStat{Total: 1263, Online: 1240, Offline: 23}},
		&stubEnergy{stat: provider.EnergyStat{TotalKwh: 12483.5, TotalWater: 321.2}}
}

// newTestCtx 构造仅含聚合所需依赖的 ServiceContext.
// Redis 传 nil 时聚合层自动跳过缓存(与生产降级行为一致).
func newTestCtx(wo provider.WorkOrderProvider, al provider.AlarmProvider,
	dv provider.DeviceProvider, en provider.EnergyProvider) *svc.ServiceContext {
	return &svc.ServiceContext{
		Providers: svc.Providers{WorkOrder: wo, Alarm: al, Device: dv, Energy: en},
	}
}

// ---------- 1. 全部成功 ----------

func TestOverview_AllSourcesOK(t *testing.T) {
	wo, al, dv, en := okStubs()
	l := NewOverviewLogic(context.Background(), newTestCtx(wo, al, dv, en))

	resp, err := l.Overview(overviewReqStub)
	if err != nil {
		t.Fatalf("Overview 不应返回错误: %v", err)
	}
	if resp.WorkOrder == nil || resp.Alarm == nil || resp.Device == nil || resp.Energy == nil {
		t.Fatalf("四路全部成功时任何卡片都不应为 nil: %+v", resp)
	}
	if len(resp.Degraded) != 0 {
		t.Errorf("Degraded = %v, 期望为空", resp.Degraded)
	}
	if resp.WorkOrder.TodayTotal != 12 || resp.WorkOrder.Unfinished != 7 {
		t.Errorf("工单卡片数值不符: %+v", resp.WorkOrder)
	}
	if resp.WorkOrder.AvgHandleSec == nil || *resp.WorkOrder.AvgHandleSec != 3600.5 {
		t.Errorf("AvgHandleSec 应为 3600.5: %+v", resp.WorkOrder.AvgHandleSec)
	}
	if resp.Device.Online != 1240 || resp.Device.Offline != 23 {
		t.Errorf("设备卡片数值不符: %+v", resp.Device)
	}
	if resp.Alarm.Critical != 1 || resp.Alarm.Major != 2 {
		t.Errorf("告警卡片数值不符: %+v", resp.Alarm)
	}
	if resp.Cached {
		t.Error("首次请求不应命中缓存")
	}
}

// ---------- 2. 单路失败 -> 字段 null + degraded, 整体不报错 ----------

func TestOverview_PartialDegradation(t *testing.T) {
	wo, al, dv, en := okStubs()
	al.err = errors.New("alarm service unavailable") // 只有告警路挂了
	l := NewOverviewLogic(context.Background(), newTestCtx(wo, al, dv, en))

	resp, err := l.Overview(overviewReqStub)
	if err != nil {
		t.Fatalf("单路失败时整体不应报错(这是本接口的硬性验收标准): %v", err)
	}
	if resp.Alarm != nil {
		t.Errorf("失败的那一路字段应为 null, 实际: %+v", resp.Alarm)
	}
	if len(resp.Degraded) != 1 || resp.Degraded[0] != sourceAlarm {
		t.Errorf("Degraded = %v, 期望 [alarm]", resp.Degraded)
	}
	// 其余三路不受牵连 —— 这验证了"goroutine 内部吸收错误"的关键设计
	if resp.WorkOrder == nil || resp.Device == nil || resp.Energy == nil {
		t.Errorf("单路失败不应拖垮其余三路: wo=%v dv=%v en=%v", resp.WorkOrder, resp.Device, resp.Energy)
	}
}

// ---------- 3. 全部失败 -> 全部 null, 仍不报错 ----------

func TestOverview_AllSourcesFail(t *testing.T) {
	fail := errors.New("all down")
	l := NewOverviewLogic(context.Background(), newTestCtx(
		&stubWorkOrder{err: fail},
		&stubAlarm{err: fail},
		&stubDevice{err: fail},
		&stubEnergy{err: fail},
	))

	resp, err := l.Overview(overviewReqStub)
	if err != nil {
		t.Fatalf("全部失败时也不应返回错误: %v", err)
	}
	if resp.WorkOrder != nil || resp.Alarm != nil || resp.Device != nil || resp.Energy != nil {
		t.Error("全部失败时四张卡片都应为 null")
	}
	if len(resp.Degraded) != 4 {
		t.Errorf("Degraded = %v, 期望 4 项", resp.Degraded)
	}
}

// ---------- 4. 慢数据源超时 -> 降级, 且整体耗时被预算兜住 ----------

func TestOverview_SlowSourceTimeout(t *testing.T) {
	wo, al, _, en := okStubs()
	// 设备路阻塞 5s, 远超 sourceTimeout(600ms), 应被超时砍掉
	l := NewOverviewLogic(context.Background(), newTestCtx(wo, al,
		&stubDevice{block: 5 * time.Second}, en))

	start := time.Now()
	resp, err := l.Overview(overviewReqStub)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("慢源超时不应返回错误: %v", err)
	}
	if resp.Device != nil {
		t.Errorf("超时的数据源应降级为 null, 实际: %+v", resp.Device)
	}
	if len(resp.Degraded) != 1 || resp.Degraded[0] != sourceDevice {
		t.Errorf("Degraded = %v, 期望 [device]", resp.Degraded)
	}
	// 整体耗时必须被 overviewTimeout(800ms) 兜住, 而不是等满 5s
	if elapsed > 2*time.Second {
		t.Errorf("整体耗时 %v, 慢源没有被超时预算砍掉", elapsed)
	}
	// 其余三路照常返回
	if resp.WorkOrder == nil || resp.Energy == nil {
		t.Error("慢源超时不应影响其余数据源")
	}
}

// ---------- 5. 缓存命中(依赖本地 Redis, 不可用则跳过) ----------

// openTestRedis 连接服务自身的 etc/dashboard-api.yaml 所指的本地 Redis.
func openTestRedis(t *testing.T) *redisx.Client {
	t.Helper()
	rdb := redisx.NewClient(&redisx.RedisConf{
		Addr: "127.0.0.1:6380",
		Pass: "onepark123",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过: 本地 Redis 不可用: %v", err)
	}
	return rdb
}

func TestOverview_CacheHit(t *testing.T) {
	rdb := openTestRedis(t)
	// 用纳秒级唯一租户, 避免与其它测试/线上 key 冲突
	tenantId := time.Now().UnixNano()

	wo, al, dv, en := okStubs()
	svcCtx := newTestCtx(wo, al, dv, en)
	svcCtx.Redis = rdb
	l := NewOverviewLogic(context.Background(), svcCtx)

	key := overviewCacheKey(tenantId)
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	// 第一次: 未命中, 走聚合
	first, err := l.Overview(tenantReq(tenantId))
	if err != nil {
		t.Fatalf("首次请求失败: %v", err)
	}
	if first.Cached {
		t.Fatal("首次请求不应命中缓存")
	}

	// 第二次: 应命中缓存, 且数值一致
	second, err := l.Overview(tenantReq(tenantId))
	if err != nil {
		t.Fatalf("二次请求失败: %v", err)
	}
	if !second.Cached {
		t.Fatal("30s 内的二次请求应命中缓存")
	}
	if second.WorkOrder == nil || second.WorkOrder.TodayTotal != first.WorkOrder.TodayTotal {
		t.Errorf("缓存数据与首次不一致: first=%+v second=%+v", first.WorkOrder, second.WorkOrder)
	}
}

// ---------- 辅助 ----------

// overviewReqStub 通用请求(TenantId=0).
var overviewReqStub = &types.OverviewReq{TenantId: 0}

// tenantReq 生成带租户的请求.
func tenantReq(tenantId int64) *types.OverviewReq { return &types.OverviewReq{TenantId: tenantId} }
