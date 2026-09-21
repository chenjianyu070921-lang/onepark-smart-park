package dashboard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"onepark/app/dashboard-service/internal/provider"
	"onepark/app/dashboard-service/internal/types"
)

// countingWorkOrder 统计"上游被真正调用了几次".
type countingWorkOrder struct {
	calls *int64
	block time.Duration
	stat  provider.WorkOrderStat
}

func (s *countingWorkOrder) Stat(ctx context.Context, _ int64) (provider.WorkOrderStat, error) {
	atomic.AddInt64(s.calls, 1)
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return provider.WorkOrderStat{}, ctx.Err()
		}
	}
	return s.stat, nil
}

// TestOverview_StampedeProtection 缓存过期瞬间的并发穿透, 只应真正聚合一次.
//
// 这是缓存击穿防护(singleflight)的核心断言: 没有它的话, N 个并发请求
// 会各自跑一遍四路聚合(500 并发 = 2000 次上游调用), 缓存"减轻 gRPC 压力"的意义被抵消。
//
// 两个刻意的设置:
//   - **不接 Redis**(newTestCtx 不设 Redis): 缓存不生效, 否则第一次写完缓存后
//     后续请求直接命中, 测不出击穿防护本身;
//   - **桩阻塞 200ms**: 让所有并发请求都堵在 singleflight 门口。若不阻塞,
//     先到的请求可能已经聚合完, 后来的会各自再聚一次, 断言就不稳定了。
func TestOverview_StampedeProtection(t *testing.T) {
	var calls int64
	wo := &countingWorkOrder{
		calls: &calls,
		block: 200 * time.Millisecond,
		stat:  provider.WorkOrderStat{TodayTotal: 5},
	}
	svcCtx := newTestCtx(wo, &stubAlarm{}, &stubDevice{}, &stubEnergy{})

	const n = 50
	start := make(chan struct{}) // 起跑线: 保证所有 goroutine 真正同时出发
	results := make([]*types.OverviewResp, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx], errs[idx] = NewOverviewLogic(context.Background(), svcCtx).
				Overview(overviewReqStub)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发请求失败: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("第 %d 个并发请求返回空响应", i)
		}
	}

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("上游被调用 %d 次, 期望 1 —— singleflight 应把同租户并发汇合为一次聚合", got)
	}

	// 每个调用方必须拿到自己的副本: singleflight 的返回值是共享的,
	// 若直接在其中改 Cached/ElapsedMs 就是 data race(配合 -race 可复现)。
	if results[0] == results[1] {
		t.Error("并发调用方拿到了同一个响应对象 —— finish 没做拷贝")
	}

	// 跟随方的语义: 数据是刚聚合出来的(非缓存), 耗时是它自己的等待时间
	if results[0].Cached || results[1].Cached {
		t.Error("未经缓存的聚合结果 Cached 应为 false")
	}
}
