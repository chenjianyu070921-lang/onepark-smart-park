package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/common/redisx"
)

// 基准里关掉日志: 降级路径**每次调用都会打一条 error**(那是生产该有的行为),
// 不关的话 benchmark 输出会被几万行 JSON 淹没 —— 而我们要看的是 ns/op。
// 仅作用于测试二进制, 不影响任何生产路径。
func init() { logx.Disable() }

// 大屏聚合性能基线 —— **聚合层自身开销**。
//
// ⚠️ 口径澄清(否则数字会被误读): 本文件用测试桩替代四路真实 gRPC,
// 量的是「并发编排 + 降级判定 + 缓存读写」这段代码的净开销,
// **不含**网络往返与上游服务耗时 —— 那些由 `tools/dashload` 对真实服务压测产出。
// 两者必须分开看: 把桩的耗时当成"大屏接口的响应时间"就是自欺。
//
// 运行:
//
//	go test ./app/dashboard-service/internal/logic/dashboard/... -run '^$' -bench . -benchmem
//
// 缓存相关基准需要本地 Redis; 连不上会 **跳过并打印原因**(不会静默算过)。

// openBenchRedis 连本地 Redis; 连不上则跳过该基准。
// 与 overview_test.go 的 openTestRedis 同源, 只是换成了 *testing.B。
func openBenchRedis(b *testing.B) *redisx.Client {
	b.Helper()
	rdb := redisx.NewClient(&redisx.RedisConf{
		Addr: "127.0.0.1:6380",
		Pass: "onepark123",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		b.Skipf("跳过缓存基准: 本地 Redis 不可用(%v)", err)
	}
	return rdb
}

// benchCtx 构造只有桩数据源、**不含 Redis** 的 ServiceContext。
// 无缓存 -> 每次迭代都是完整聚合, 量到的才是聚合层净开销。
func benchCtx(wo *stubWorkOrder, al *stubAlarm, dv *stubDevice, en *stubEnergy) *svc.ServiceContext {
	return newTestCtx(wo, al, dv, en)
}

// BenchmarkOverview_FourSourcesOK 四路全部正常 —— 最理想路径。
func BenchmarkOverview_FourSourcesOK(b *testing.B) {
	wo, al, dv, en := okStubs()
	l := NewOverviewLogic(context.Background(), benchCtx(wo, al, dv, en))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := l.Overview(overviewReqStub)
		if err != nil {
			b.Fatalf("Overview 不应返回错误: %v", err)
		}
		if resp.Alarm == nil || resp.Device == nil {
			b.Fatal("四路正常时不应出现降级卡片")
		}
	}
}

// BenchmarkOverview_OneSourceDown 一路挂(其余正常)。
//
// 这条最有价值: 降级路径必须在"一路失败"时既不报错、也**不显著变慢** ——
// 若明显慢于四路正常, 说明降级处理本身成了瓶颈。
func BenchmarkOverview_OneSourceDown(b *testing.B) {
	wo, al, dv, en := okStubs()
	al.err = errors.New("alarm-service unreachable")
	l := NewOverviewLogic(context.Background(), benchCtx(wo, al, dv, en))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := l.Overview(overviewReqStub)
		if err != nil {
			b.Fatalf("单路失败不应让整体报错: %v", err)
		}
		if resp.Alarm != nil {
			b.Fatal("挂掉的那路应为 nil")
		}
		if len(resp.Degraded) != 1 || resp.Degraded[0] != sourceAlarm {
			b.Fatalf("degraded 应恰好为 [alarm], 实际 %v", resp.Degraded)
		}
	}
}

// BenchmarkOverview_AllSourcesDown 四路全挂 —— 最坏情况(上游集体不可达)。
func BenchmarkOverview_AllSourcesDown(b *testing.B) {
	wo, al, dv, en := okStubs()
	e := errors.New("upstream unreachable")
	wo.err, al.err, dv.err, en.err = e, e, e, e
	l := NewOverviewLogic(context.Background(), benchCtx(wo, al, dv, en))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := l.Overview(overviewReqStub)
		if err != nil {
			b.Fatalf("四路全挂也必须正常返回(逐源降级, 绝不 5xx): %v", err)
		}
		if len(resp.Degraded) != 4 {
			b.Fatalf("degraded 应为 4 项, 实际 %v", resp.Degraded)
		}
	}
}

// BenchmarkOverview_Concurrent 并发调用(无缓存) —— 量聚合层在并发下的伸缩性。
// 顺序再快、并发一压就退化也没用。
func BenchmarkOverview_Concurrent(b *testing.B) {
	wo, al, dv, en := okStubs()
	l := NewOverviewLogic(context.Background(), benchCtx(wo, al, dv, en))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := l.Overview(overviewReqStub); err != nil {
				b.Fatalf("并发下不应报错: %v", err)
			}
		}
	})
}

// ---------- 缓存路径(需要真实 Redis) ----------

// BenchmarkOverview_CacheHit 缓存命中路径 —— 大屏稳态下绝大多数请求走这里。
func BenchmarkOverview_CacheHit(b *testing.B) {
	rdb := openBenchRedis(b)
	wo, al, dv, en := okStubs()
	ctx := &svc.ServiceContext{
		Providers: svc.Providers{WorkOrder: wo, Alarm: al, Device: dv, Energy: en},
		Redis:     rdb,
	}
	l := NewOverviewLogic(context.Background(), ctx)
	req := tenantReq(time.Now().UnixNano())

	// 先跑一次把缓存灌上
	if _, err := l.Overview(req); err != nil {
		b.Fatalf("预热失败: %v", err)
	}
	b.Cleanup(func() { _, _ = InvalidateOverviewCache(context.Background(), ctx) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := l.Overview(req)
		if err != nil {
			b.Fatalf("命中缓存时不应报错: %v", err)
		}
		if !resp.Cached {
			// 缓存过期(30s)后基准会中途变成未命中 —— 据实报错, 不忽略
			b.Fatalf("第 %d 次迭代未命中缓存(基准时长超过缓存 TTL, 请用 -benchtime 缩短)", i)
		}
	}
}

// BenchmarkOverview_CacheHitWithSlowUpstreams 上游各 5ms(模拟真实 gRPC 往返), 命中缓存。
//
// 为什么要这一对: 用零耗时桩测出来「缓存比聚合还慢」→ 会得出"缓存是负优化"的**错误结论** ——
// 那不是缓存慢, 是桩太快(真实上游是毫秒级网络调用, 而 Redis 是同一个内网里的一次往返)。
// 所以要把上游延迟还原出来再比:
//
//	命中(0.5ms Redis 往返) vs 未命中(5ms 上游 + 0.5ms Redis) -> 才是缓存真实的收益。
func BenchmarkOverview_CacheHitWithSlowUpstreams(b *testing.B) {
	rdb := openBenchRedis(b)
	wo, al, dv, en := okStubs()
	wo.block, al.block, dv.block, en.block = 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond
	ctx := &svc.ServiceContext{
		Providers: svc.Providers{WorkOrder: wo, Alarm: al, Device: dv, Energy: en},
		Redis:     rdb,
	}
	l := NewOverviewLogic(context.Background(), ctx)
	req := tenantReq(time.Now().UnixNano())
	if _, err := l.Overview(req); err != nil { // 预热
		b.Fatalf("预热失败: %v", err)
	}
	b.Cleanup(func() { _, _ = InvalidateOverviewCache(context.Background(), ctx) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Overview(req); err != nil {
			b.Fatalf("命中缓存时不应报错: %v", err)
		}
	}
}

// BenchmarkOverview_CacheMissWithSlowUpstreams 上游各 5ms + 每次都不命中(缓存失效)。
// 与上一个基准配对, 差值即缓存在**接近真实上游延迟**下省下的时间。
func BenchmarkOverview_CacheMissWithSlowUpstreams(b *testing.B) {
	rdb := openBenchRedis(b)
	wo, al, dv, en := okStubs()
	wo.block, al.block, dv.block, en.block = 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond
	ctx := &svc.ServiceContext{
		Providers: svc.Providers{WorkOrder: wo, Alarm: al, Device: dv, Energy: en},
		Redis:     rdb,
	}
	l := NewOverviewLogic(context.Background(), ctx)
	req := tenantReq(time.Now().UnixNano())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := InvalidateOverviewCache(context.Background(), ctx); err != nil {
			b.Fatalf("失效缓存失败: %v", err)
		}
		b.StartTimer()
		if _, err := l.Overview(req); err != nil {
			b.Fatalf("未命中路径不应报错: %v", err)
		}
	}
}

// BenchmarkOverview_CacheMissThenAggregate 每次都不命中(先失效再取)。
// 与 CacheHit 的差值 = 缓存收益(注意: 这里上游是零耗时桩, 收益会被低估 —— 见 SlowUpstreams 那对)。
func BenchmarkOverview_CacheMissThenAggregate(b *testing.B) {
	rdb := openBenchRedis(b)
	wo, al, dv, en := okStubs()
	ctx := &svc.ServiceContext{
		Providers: svc.Providers{WorkOrder: wo, Alarm: al, Device: dv, Energy: en},
		Redis:     rdb,
	}
	l := NewOverviewLogic(context.Background(), ctx)
	req := tenantReq(time.Now().UnixNano())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := InvalidateOverviewCache(context.Background(), ctx); err != nil {
			b.Fatalf("失效缓存失败: %v", err)
		}
		b.StartTimer()

		if _, err := l.Overview(req); err != nil {
			b.Fatalf("未命中路径不应报错: %v", err)
		}
	}
}
