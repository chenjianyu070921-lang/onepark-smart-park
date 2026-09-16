package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"golang.org/x/sync/errgroup"

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

// Overview 并行拉取 4 路数据源并聚合.
//
// 降级约定(组长验收口径): 某一路失败时该字段返回 null 并在 degraded 中列出,
// 接口整体始终返回 200, 绝不因单路失败抛出 5xx.
func (l *OverviewLogic) Overview(req *types.OverviewReq) (*types.OverviewResp, error) {
	start := time.Now()
	cacheKey := overviewCacheKey(req.TenantId)

	if cached, ok := l.readCache(cacheKey); ok {
		cached.Cached = true
		cached.ElapsedMs = time.Since(start).Milliseconds()
		return cached, nil
	}

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
	resp.ElapsedMs = time.Since(start).Milliseconds()

	l.writeCache(cacheKey, resp)

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
