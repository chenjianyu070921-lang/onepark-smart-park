package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
)

const (
	// overviewTimeout 聚合接口整体预算, 保证大屏响应体验.
	overviewTimeout = 800 * time.Millisecond
	// sourceTimeout 单个数据源预算, 避免慢数据源拖垮整体.
	sourceTimeout = 600 * time.Millisecond
	// overviewCacheTTL 概览缓存时长(30s, 组长口径).
	overviewCacheTTL = 30 * time.Second
)

// 数据源标识, 用于响应中的 degraded 列表.
const (
	sourceWorkOrder = "work_order"
	sourceAlarm     = "alarm"
	sourceDevice    = "device"
	sourceEnergy    = "energy"
)

// OverviewLogic 综合查询: 并行聚合多数据源, 逐源独立降级.
type OverviewLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewOverviewLogic 构造综合查询逻辑.
func NewOverviewLogic(ctx context.Context, svcCtx *svc.ServiceContext) *OverviewLogic {
	return &OverviewLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// overviewSF 缓存击穿防护: 同一租户的并发请求只放一个进真正的聚合。
//
// 不这么做的后果很具体: 30s 缓存过期的那一瞬间, 若正好有 N 个并发请求到达,
// N 个请求会**同时穿透到四路 gRPC**(500 并发 = 2000 次上游调用) ——
// 恰好抵消了缓存"减轻 gRPC 压力"的意义。
//
// 必须是包级变量: OverviewLogic 是每请求构造的, 挂在实例上等于没有防护。
var overviewSF singleflight.Group

// overviewOutcome 聚合结果 + 它是否来自缓存.
type overviewOutcome struct {
	resp      *types.OverviewResp
	fromCache bool
}

// finish 返回一份副本, 并写入"本次请求特有"的两个字段.
//
// 必须拷贝: singleflight 的返回值被多个并发调用方共享, 直接在上面改
// Cached / ElapsedMs 就是数据竞争。浅拷贝足够 —— 这里只改值字段,
// 不碰 Device/Alarm/Energy/WorkOrder 这些被共享的指针所指的对象。
//
// 跟随方(没能执行聚合的那一个)看到 Cached=false 且耗时很短,
// 语义是准确的: 数据是刚聚合出来的(不是缓存), 只是不是我算的。
func finish(resp *types.OverviewResp, fromCache bool, start time.Time) *types.OverviewResp {
	out := *resp
	out.Cached = fromCache
	out.ElapsedMs = time.Since(start).Milliseconds()
	return &out
}

// Overview 并行拉取 4 路数据源并聚合.
//
// 读序: 缓存 -> 击穿防护 -> 双检缓存 -> 真正聚合.
//
// 降级约定(组长验收口径): 某一路失败时该字段返回 null 并在 degraded 中列出,
// 接口整体始终返回 200, 绝不因单路失败抛出 5xx.
func (l *OverviewLogic) Overview(req *types.OverviewReq) (*types.OverviewResp, error) {
	start := time.Now()
	cacheKey := overviewCacheKey(req.TenantId)

	if cached, ok := l.readCache(cacheKey); ok {
		return finish(cached, true, start), nil
	}

	// 同一租户的并发在这里汇合: 只有第一个调用方会真正执行下面的函数体
	v, err, _ := overviewSF.Do(cacheKey, func() (interface{}, error) {
		// 双检: 等在门外的期间, 可能已有前一个请求写好了缓存, 那就别再聚合一次
		if cached, ok := l.readCache(cacheKey); ok {
			return &overviewOutcome{resp: cached, fromCache: true}, nil
		}
		resp, aerr := l.aggregate(req)
		if aerr != nil {
			return nil, aerr
		}
		l.writeCache(cacheKey, resp)
		return &overviewOutcome{resp: resp}, nil
	})
	if err != nil {
		return nil, err
	}
	outcome, ok := v.(*overviewOutcome)
	if !ok || outcome == nil || outcome.resp == nil {
		return nil, fmt.Errorf("overview 聚合结果类型异常")
	}

	return finish(outcome.resp, outcome.fromCache, start), nil
}

// aggregate 真正执行四路并发聚合(不含缓存读写与耗时统计).
func (l *OverviewLogic) aggregate(req *types.OverviewReq) (*types.OverviewResp, error) {
	resp := &types.OverviewResp{}
	var (
		mu       sync.Mutex
		degraded []string
	)
	markDegraded := func(source string) {
		mu.Lock()
		degraded = append(degraded, source)
		mu.Unlock()
	}

	// 外层 ctx 只承担"整体超时"职责; 各路 goroutine 内部吸收错误并返回 nil,
	// 避免 errgroup 因单路失败取消其余分支, 把"部分降级"变成"整体失败".
	ctx, cancel := context.WithTimeout(l.ctx, overviewTimeout)
	defer cancel()

	var g errgroup.Group

	g.Go(func() error {
		sctx, c := context.WithTimeout(ctx, sourceTimeout)
		defer c()
		stat, err := l.svcCtx.Providers.WorkOrder.Stat(sctx, req.TenantId)
		if err != nil {
			l.Errorf("[overview] source %s failed: %v", sourceWorkOrder, err)
			markDegraded(sourceWorkOrder)
			return nil
		}
		resp.WorkOrder = &types.WorkOrderCard{
			Available:    true,
			TodayTotal:   stat.TodayTotal,
			Unfinished:   stat.Unfinished,
			AvgHandleSec: stat.AvgHandleSec,
			CompleteRate: stat.CompleteRate,
		}
		return nil
	})

	g.Go(func() error {
		sctx, c := context.WithTimeout(ctx, sourceTimeout)
		defer c()
		stat, err := l.svcCtx.Providers.Alarm.Stat(sctx, req.TenantId)
		if err != nil {
			l.Errorf("[overview] source %s failed: %v", sourceAlarm, err)
			markDegraded(sourceAlarm)
			return nil
		}
		resp.Alarm = &types.AlarmCard{
			Total:    stat.Total,
			Critical: stat.Critical,
			Major:    stat.Major,
			Minor:    stat.Minor,
			Info:     stat.Info,
		}
		return nil
	})

	g.Go(func() error {
		sctx, c := context.WithTimeout(ctx, sourceTimeout)
		defer c()
		stat, err := l.svcCtx.Providers.Device.Stat(sctx)
		if err != nil {
			l.Errorf("[overview] source %s failed: %v", sourceDevice, err)
			markDegraded(sourceDevice)
			return nil
		}
		resp.Device = &types.DeviceCard{
			Total:   stat.Total,
			Online:  stat.Online,
			Offline: stat.Offline,
			Fault:   stat.Fault,
		}
		return nil
	})

	g.Go(func() error {
		sctx, c := context.WithTimeout(ctx, sourceTimeout)
		defer c()
		stat, err := l.svcCtx.Providers.Energy.Stat(sctx)
		if err != nil {
			l.Errorf("[overview] source %s failed: %v", sourceEnergy, err)
			markDegraded(sourceEnergy)
			return nil
		}
		resp.Energy = &types.EnergyCard{
			TotalKwh:   stat.TotalKwh,
			TotalWater: stat.TotalWater,
		}
		return nil
	})

	_ = g.Wait()

	resp.Degraded = degraded
	resp.UpdatedAt = time.Now().Unix()
	// ElapsedMs / Cached 刻意不在这里写: 这个对象会被 singleflight 共享给多个
	// 并发调用方, 在这里写就是数据竞争; 由调用方在 finish 里按"本次请求"填写。

	return resp, nil
}

// overviewCacheKey 按租户维度隔离聚合缓存.
func overviewCacheKey(tenantId int64) string {
	return fmt.Sprintf("m5:dashboard:overview:tenant:%d", tenantId)
}

// readCache 读取聚合缓存; 未命中或反序列化失败时返回 ok=false.
func (l *OverviewLogic) readCache(key string) (*types.OverviewResp, bool) {
	if l.svcCtx.Redis == nil {
		return nil, false
	}
	val, err := l.svcCtx.Redis.Get(l.ctx, key).Result()
	if err != nil || val == "" {
		return nil, false
	}
	var resp types.OverviewResp
	if err := json.Unmarshal([]byte(val), &resp); err != nil {
		l.Errorf("[overview] unmarshal cache failed: %v", err)
		return nil, false
	}
	return &resp, true
}

// writeCache 写入聚合缓存; 失败只记日志, 不影响本次返回.
func (l *OverviewLogic) writeCache(key string, resp *types.OverviewResp) {
	if l.svcCtx.Redis == nil {
		return
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		l.Errorf("[overview] marshal cache failed: %v", err)
		return
	}
	if err := l.svcCtx.Redis.Set(l.ctx, key, buf, overviewCacheTTL).Err(); err != nil {
		l.Errorf("[overview] write cache failed: %v", err)
	}
}
