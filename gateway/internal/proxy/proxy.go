// Package proxy 实现基于路径前缀的反向代理, 作为 OnePark 对外统一入口(网关).
// 职责: 按最长前缀匹配转发请求到上游服务; 注入 x-request-id 与 RBAC 上下文
// (x-tenant-id/x-user-id/x-role-ids); 统一 CORS; 上游不可用时返回 502 统一响应体.
//
// 增强能力(本文件):
//   - 熔断(Circuit Breaking): 每个上游路由内置熔断器, 连续失败达阈值后"断开",
//     冷却期后进入"半开"放一个探测; 断开期间请求快速失败返回 503, 避免雪崩.
//   - 灰度(Canary): 上游可配置 CanaryTarget + 权重/Header, 按比例或按 Header 将
//     部分流量导向灰度实例(灰度目标同样受熔断保护).
package proxy

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	mrand "math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"onepark/common/ctxdata"
	"onepark/gateway/internal/config"
)

// 熔断器默认参数.
const (
	breakerThreshold = 5       // 连续失败达到该值后断开
	breakerCooldown  = 10 * time.Second // 断开后冷却时长, 之后进入半开探测
)

// breaker 单上游熔断器(无锁, 基于原子操作).
// 状态机: closed ->(连续失败>=阈值)-> open ->(冷却结束)-> half-open ->(探测成功)-> closed
//
//	                                      ->(探测失败)-> open(刷新冷却)
type breaker struct {
	state     int32 // breakerState
	failCount int32
	openedAt  int64 // 进入 open 的时间戳(纳秒)
	threshold int32 // 连续失败断开阈值(来自全局配置, 默认 breakerThreshold)
	cooldown  int64 // 断开后冷却时长(纳秒, 来自全局配置, 默认 breakerCooldown)
}

type breakerState int32

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// newBreaker 创建单上游熔断器. threshold/cooldown 来自全局配置(已由 Gateway 收敛默认值).
func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{
		state:     int32(stateClosed),
		threshold: int32(threshold),
		cooldown:  cooldown.Nanoseconds(),
	}
}

// allow 判断是否放行本次请求. 仅在 open->half-open 的瞬间通过 CAS 放一个探测,
// 其余 open 请求直接拒绝; 半开状态(探测在飞)也拒绝.
func (b *breaker) allow(now time.Time) bool {
	switch breakerState(atomic.LoadInt32(&b.state)) {
	case stateClosed:
		return true
	case stateOpen:
		if now.UnixNano()-atomic.LoadInt64(&b.openedAt) >= b.cooldown {
			return atomic.CompareAndSwapInt32(&b.state, int32(stateOpen), int32(stateHalfOpen))
		}
		return false
	default: // half-open: 已有探测在飞, 拒绝其余请求
		return false
	}
}

// record 记录一次请求结果, 驱动状态机.
func (b *breaker) record(success bool, now time.Time) {
	if success {
		atomic.StoreInt32(&b.failCount, 0)
		atomic.StoreInt32(&b.state, int32(stateClosed))
		return
	}
	if breakerState(atomic.LoadInt32(&b.state)) == stateHalfOpen {
		atomic.StoreInt64(&b.openedAt, now.UnixNano())
		atomic.StoreInt32(&b.state, int32(stateOpen))
		return
	}
	if atomic.AddInt32(&b.failCount, 1) >= b.threshold {
		atomic.StoreInt64(&b.openedAt, now.UnixNano())
		atomic.StoreInt32(&b.state, int32(stateOpen))
	}
}

// route 单条前缀路由(含熔断与灰度).
type route struct {
	prefix       string
	proxy        *httputil.ReverseProxy // 主上游
	canary       *httputil.ReverseProxy // 灰度上游(可选)
	canaryWeight int                    // 灰度权重 0-100(百分比)
	canaryHeader string                 // 灰度命中 Header 名(可选)
	breaker      *breaker
}

// isCanary 依据 Header 或权重判定本次请求是否走灰度.
func (rt route) isCanary(r *http.Request) bool {
	if rt.canaryHeader != "" {
		if v := r.Header.Get(rt.canaryHeader); v != "" && v != "false" {
			return true
		}
	}
	if rt.canaryWeight > 0 && mrand.Intn(100) < rt.canaryWeight {
		return true
	}
	return false
}

// handle 执行熔断判定 + 灰度选择 + 转发, 并记录结果驱动熔断器.
func (rt route) handle(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if !rt.breaker.allow(now) {
		writeJSONError(w, http.StatusServiceUnavailable, "M6-E-0007",
			"upstream circuit open ["+rt.prefix+"]: retry later")
		return
	}

	target := rt.proxy
	if rt.canary != nil && rt.isCanary(r) {
		target = rt.canary
	}

	// statusRecorder 仅捕获响应状态码(不缓冲 body), 用于熔断判定;
	// 透传 Flush/Hijack, 保证 WebSocket 升级与流式响应不受影响.
	rec := &statusRecorder{ResponseWriter: w}
	target.ServeHTTP(rec, r)

	success := rec.status > 0 && rec.status < 500
	rt.breaker.record(success, now)
}

// Gateway 对外网关(实现 http.Handler).
// routes 以 atomic 指针持有, 支持 Nacos 配置热更新时原子替换, 无需重启.
type Gateway struct {
	routes           atomic.Pointer[[]route] // 按 prefix 长度降序, 保证最长前缀优先
	defaultTenantID  int64
	defaultUserId    int64
	breakerThreshold int          // 全局熔断阈值(已收敛默认值)
	breakerCooldown  time.Duration // 全局熔断冷却(已收敛默认值)
}

// NewGateway 依据配置构建网关及其上游反向代理.
func NewGateway(c config.Config) (*Gateway, error) {
	// 收敛熔断默认值: 配置<=0(误配/未填)时回落到常量默认, 避免熔断被关掉.
	threshold := c.BreakerThreshold
	if threshold <= 0 {
		threshold = breakerThreshold
	}
	cooldown := c.BreakerCooldown
	if cooldown <= 0 {
		cooldown = breakerCooldown
	}
	g := &Gateway{
		defaultTenantID:  c.DefaultTenantId,
		defaultUserId:    c.DefaultUserId,
		breakerThreshold: threshold,
		breakerCooldown:  cooldown,
	}
	if err := g.Reload(c.Upstreams); err != nil {
		return nil, err
	}
	return g, nil
}

// Reload 用新的上游表重建路由(原子替换, 供 Nacos 配置热更新调用).
func (g *Gateway) Reload(upstreams []config.UpstreamConf) error {
	routes := make([]route, 0, len(upstreams))
	for _, up := range upstreams {
		target, err := url.Parse(up.Target)
		if err != nil {
			return err
		}
		prefix := up.Prefix
		rp := httputil.NewSingleHostReverseProxy(target)
		rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			writeJSONError(w, http.StatusBadGateway, "M6-E-0006", "upstream unavailable ["+prefix+"]: "+e.Error())
		}

		// 熔断阈值: 路由级 BreakerThreshold>0 时覆盖全局默认, 否则回落全局(与 NewGateway 收敛同源).
		th := g.breakerThreshold
		if up.BreakerThreshold > 0 {
			th = up.BreakerThreshold
		}
		rt := route{
			prefix:  prefix,
			proxy:   rp,
			breaker: newBreaker(th, g.breakerCooldown),
		}

		// 灰度上游(可选): 与主上游共享同一熔断器, 灰度目标不可用时同样快速失败.
		if up.CanaryTarget != "" {
			ct, err := url.Parse(up.CanaryTarget)
			if err != nil {
				return err
			}
			crp := httputil.NewSingleHostReverseProxy(ct)
			crp.ErrorHandler = rp.ErrorHandler
			rt.canary = crp
			rt.canaryWeight = up.CanaryWeight
			rt.canaryHeader = up.CanaryHeader
		}

		routes = append(routes, rt)
	}
	// 最长前缀优先匹配.
	sort.Slice(routes, func(i, j int) bool {
		return len(routes[i].prefix) > len(routes[j].prefix)
	})
	g.routes.Store(&routes)
	return nil
}

// ServeHTTP 处理全部入站请求: CORS -> 注入上下文头 -> 最长前缀匹配转发.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 开发环境 CORS 全放通.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, X-Request-Id, X-Tenant-Id, X-User-Id, X-Role-Ids")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 注入 RequestId(透传或生成) 与 RBAC 上下文(缺省用配置默认值, 便于无鉴权联调).
	rid := r.Header.Get(ctxdata.CtxRequestId)
	if rid == "" {
		rid = newRequestId()
		r.Header.Set(ctxdata.CtxRequestId, rid)
	}
	w.Header().Set(ctxdata.CtxRequestId, rid)
	if r.Header.Get(ctxdata.CtxTenantId) == "" {
		r.Header.Set(ctxdata.CtxTenantId, strconv.FormatInt(g.defaultTenantID, 10))
	}
	if r.Header.Get(ctxdata.CtxUserId) == "" {
		r.Header.Set(ctxdata.CtxUserId, strconv.FormatInt(g.defaultUserId, 10))
	}

	// 最长前缀匹配转发(路由表可能已被 Nacos 热更新原子替换).
	routes := *g.routes.Load()
	for _, rt := range routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			rt.handle(w, r)
			return
		}
	}

	writeJSONError(w, http.StatusNotFound, "M6-E-0004", "no upstream route for path: "+r.URL.Path)
}

// statusRecorder 包裹 ResponseWriter, 仅记录响应状态码(不缓冲 body),
// 用于熔断判定; 透传 Flush 与 Hijack 以保证流式/WebSocket 正常.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// writeJSONError 以统一响应体 {code,msg,data} 输出网关层错误.
func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": code,
		"msg":  msg,
		"data": nil,
	})
}

// newRequestId 生成简易 RequestId(网关层兜底).
func newRequestId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "rid-" + hex.EncodeToString(b)
}
