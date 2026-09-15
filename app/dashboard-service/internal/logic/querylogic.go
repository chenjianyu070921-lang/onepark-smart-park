package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"onepark/common/ctxdata"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	queryCachePrefix = "dashboard:query:"
	queryCacheTTL    = 30 * time.Second
)

// QueryLogic 综合查询聚合逻辑.
type QueryLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewQueryLogic(ctx context.Context, svcCtx *svc.ServiceContext) *QueryLogic {
	return &QueryLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Query 并行聚合多数据源; 每个数据源独立 goroutine + 超时, 失败仅降级为 null + 标记,
// 绝不让整个接口 500. 当前仅 workorder(M2) 已就绪, 其余数据源(M3/M1/M2 其余域)接入后填充.
func (l *QueryLogic) Query() (resp *types.QueryResp, err error) {
	tenant := ctxdata.GetTenantId(l.ctx)
	resp = &types.QueryResp{TenantId: tenant}

	// 1) 命中缓存(带租户维度, 短超时避免 Redis 不可用时阻塞).
	cacheKey := queryCachePrefix + fmt.Sprintf("%d", tenant)
	rctx, rcancel := context.WithTimeout(l.ctx, cacheTimeout)
	defer rcancel()
	if data, cErr := l.svcCtx.Redis.Get(rctx, cacheKey).Bytes(); cErr == nil {
		if json.Unmarshal(data, resp) == nil {
			resp.Cached = true
			return resp, nil
		}
	}

	markers := make([]string, 0)
	var mu sync.Mutex
	appendMarker := func(m string) {
		mu.Lock()
		markers = append(markers, m)
		mu.Unlock()
	}

	var wg sync.WaitGroup

	// 工单(M2, 已就绪) —— 真实数据.
	wg.Add(1)
	go func() {
		defer wg.Done()
		wo, e := fetchWorkOrderStat(l.ctx, l.svcCtx.WorkorderRPC, tenant)
		if e != nil {
			l.Logger.Errorf("query workorder degraded: %v", e)
			appendMarker("workorder")
			return
		}
		resp.WorkOrder = wo
	}()

	// 告警(M3 未就绪) —— 降级为 null + 标记.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// TODO(M3): 接入 GetActiveAlarms 后填充 AlarmStat.
		appendMarker("alarm")
	}()

	// 设备(M1 未就绪) —— 降级为 null + 标记.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// TODO(M1): 接入 ListDevices/GetDeviceStatus 后填充 DeviceStat.
		appendMarker("device")
	}()

	// 停车(M2 parking 未就绪) —— 降级为 null + 标记.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// TODO(M2): 接入 parking-service 统计后填充 ParkingStat.
		appendMarker("parking")
	}()

	// 公告(M2 notice 未就绪) —— 降级为 null + 标记.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// TODO(M2): 接入 notice-service 统计后填充 NoticeStat.
		appendMarker("notice")
	}()

	wg.Wait()
	resp.FallbackMarkers = markers

	// 2) 写回缓存(含降级结果也缓存, 避免抖动放大; 失败忽略).
	if data, mErr := json.Marshal(resp); mErr == nil {
		wctx, wcancel := context.WithTimeout(l.ctx, cacheTimeout)
		defer wcancel()
		_ = l.svcCtx.Redis.Set(wctx, cacheKey, data, queryCacheTTL).Err()
	}

	return resp, nil
}
