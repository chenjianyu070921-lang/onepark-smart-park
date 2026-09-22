package proxy

import (
	"testing"
	"time"

	"onepark/gateway/internal/config"
)

// openBreaker 把熔断器打到 open(连续失败达阈值).
func openBreaker(b *breaker) {
	for i := 0; i < breakerThreshold; i++ {
		b.record(false, time.Now())
	}
}

// TestBreakerClosedAllows 校验 closed 状态全部放行(含 WebSocket/流式长连接路径).
func TestBreakerClosedAllows(t *testing.T) {
	b := newBreaker(breakerThreshold, breakerCooldown)
	for i := 0; i < 10; i++ {
		if !b.allow(time.Now()) {
			t.Fatalf("closed 状态应放行, 第 %d 次被拒", i)
		}
	}
}

// TestBreakerOpensAfterThreshold 校验连续失败达阈值后断开(open), 后续请求快速失败.
func TestBreakerOpensAfterThreshold(t *testing.T) {
	b := newBreaker(breakerThreshold, breakerCooldown)
	openBreaker(b)
	if b.allow(time.Now()) {
		t.Fatal("连续失败达阈值后应断开(open), allow 应返回 false(对应网关 503 快速失败)")
	}
}

// TestBreakerHalfOpenProbeThenRecover 校验冷却结束后进入 half-open 放一个探测,
// 探测成功回到 closed, 且半开期间其余请求被拒(避免探测风暴).
func TestBreakerHalfOpenProbeThenRecover(t *testing.T) {
	b := newBreaker(breakerThreshold, breakerCooldown)
	openBreaker(b)

	// 冷却未到, 仍拒绝.
	if b.allow(time.Now()) {
		t.Fatal("冷却期内应仍拒绝")
	}

	// 冷却结束: 半开放一个探测.
	now := time.Now().Add(breakerCooldown + time.Second)
	if !b.allow(now) {
		t.Fatal("冷却结束后应进入 half-open 并放行一个探测")
	}
	// 探测在飞, 其余请求拒绝.
	if b.allow(now) {
		t.Fatal("half-open 仅放一个探测, 其余应拒绝")
	}

	// 探测成功 -> 回到 closed.
	b.record(true, now)
	if !b.allow(now) {
		t.Fatal("探测成功后应回到 closed 并放行")
	}
}

// TestBreakerHalfOpenProbeFailReopen 校验半开探测失败会重新打开并刷新冷却,
// 立即再次拒绝(防止在故障上游上反复探测放大雪崩).
func TestBreakerHalfOpenProbeFailReopen(t *testing.T) {
	b := newBreaker(breakerThreshold, breakerCooldown)
	openBreaker(b)
	now := time.Now().Add(breakerCooldown + time.Second)
	if !b.allow(now) {
		t.Fatal("冷却结束后应进入 half-open")
	}
	// 探测失败 -> 重新打开.
	b.record(false, now)
	if b.allow(now) {
		t.Fatal("half-open 探测失败应重新打开, 立即拒绝")
	}
}

// TestBreakerSuccessResetsFailCount 校验一次成功即重置状态与失败计数,
// 避免部分失败长期累积导致误断(重置后需重新累计到阈值才断开).
func TestBreakerSuccessResetsFailCount(t *testing.T) {
	b := newBreaker(breakerThreshold, breakerCooldown)
	b.record(false, time.Now())
	b.record(false, time.Now())
	b.record(true, time.Now()) // 成功重置

	if !b.allow(time.Now()) {
		t.Fatal("成功应重置状态为 closed")
	}
	// 重置后单次失败不应触发断开(验证 failCount 已清零).
	b.record(false, time.Now())
	if !b.allow(time.Now()) {
		t.Fatal("failCount 清零后, 单次失败不应断开")
	}
}

// TestBreakerCustomThresholdAndCooldown 校验熔断参数由构造时注入(对应全局配置透传),
// 非硬编码: 阈值=2 时两次失败即断开, 冷却=50ms 时到期进入半开.
func TestBreakerCustomThresholdAndCooldown(t *testing.T) {
	const customThreshold = 2
	const customCooldown = 50 * time.Millisecond
	b := newBreaker(customThreshold, customCooldown)

	// 阈值=2: 仅一次失败不应断开.
	b.record(false, time.Now())
	if !b.allow(time.Now()) {
		t.Fatal("自定义阈值=2 时, 单次失败不应断开")
	}
	// 第二次失败达到阈值 -> 断开.
	b.record(false, time.Now())
	if b.allow(time.Now()) {
		t.Fatal("自定义阈值=2 时, 两次失败应断开")
	}
	// 冷却=50ms: 未到不半开, 过了半开.
	if b.allow(time.Now()) {
		t.Fatal("未到冷却期应仍拒绝")
	}
	now := time.Now().Add(customCooldown + time.Millisecond)
	if !b.allow(now) {
		t.Fatal("自定义冷却=50ms 到期后应进入半开")
	}
}

// TestReloadPerRouteBreakerThreshold 校验 Reload 按路由级 BreakerThreshold 覆盖全局默认,
// 0/不填回落全局(与 NewGateway 的 <=0 收敛同源). 对应 per-route 熔断阈值改动.
func TestReloadPerRouteBreakerThreshold(t *testing.T) {
	cfg := config.Config{
		BreakerThreshold: 5, // 全局默认
		BreakerCooldown:  10 * time.Second,
		Upstreams: []config.UpstreamConf{
			{Prefix: "/api/billing", Target: "http://127.0.0.1:18099", BreakerThreshold: 3},
			{Prefix: "/api/device", Target: "http://127.0.0.1:18098"}, // 不填, 回落全局 5
		},
	}
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}
	routes := *g.routes.Load()
	if len(routes) != 2 {
		t.Fatalf("期望 2 条路由, 实际 %d", len(routes))
	}
	byPrefix := make(map[string]*route, len(routes))
	for i := range routes {
		byPrefix[routes[i].prefix] = &routes[i]
	}
	// /api/billing 取路由级 3.
	if got := byPrefix["/api/billing"].breaker.threshold; got != 3 {
		t.Fatalf("/api/billing 熔断阈值应为 3(路由级覆盖), 实际 %d", got)
	}
	// /api/device 不填回落全局 5.
	if got := byPrefix["/api/device"].breaker.threshold; got != 5 {
		t.Fatalf("/api/device 熔断阈值应为 5(回落全局), 实际 %d", got)
	}
}
