package rule

import (
	"context"
	"sync"
	"testing"
	"time"
)

// 测试替身复用 rule_test.go 的 fakeStore / mustSpec, 本文件只补 Invalidate 相关场景.
// (fakeStore 无锁, 并发用例另用 lockedStore)

// lockedStore 带锁的规则源: 并发用例下 fakeStore 的 calls++ 会触发数据竞争, 故单独定义.
type lockedStore struct {
	mu    sync.Mutex
	rules []Rule
}

func (s *lockedStore) ListEnabled(context.Context) ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rules, nil
}

// engineTempRule 构造一条阈值规则: payload.temperature > 80.
func engineTempRule(t *testing.T, id int64) Rule {
	t.Helper()
	return Rule{
		ID: id, Name: "温度超限", EventType: "temperature", Level: 3,
		Spec: mustSpec(t, `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gt","value":80}]}`),
	}
}

// engineTempEvent 一次温度 90 度的上报(必然命中上述规则).
func engineTempEvent() Fields {
	return Fields{
		EventType: "temperature", DeviceID: "dev-1",
		Payload: map[string]interface{}{"temperature": float64(90)},
	}
}

// TestEngine_InvalidateMakesRuleChangeTakeEffectImmediately 规则变更后 Invalidate 必须立即生效.
//
// 本用例存在的全部理由: 不失效时引擎会继续按旧快照判定最长 30s(ruleCacheTTL),
// 其中"运维禁用了一条正在误报的规则, 它却又报了 30 秒"最难排查 ——
// 现象与"规则没配好"完全一样, 靠日志区分不出来。
func TestEngine_InvalidateMakesRuleChangeTakeEffectImmediately(t *testing.T) {
	store := &fakeStore{rules: []Rule{engineTempRule(t, 1)}}
	e := NewEngine(store, newFakeWindow(), time.Minute) // TTL 取足够长, 排除"自然过期"的干扰
	ctx := context.Background()

	drafts, err := e.Evaluate(ctx, engineTempEvent(), "req-1")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("变更前应命中 1 条, 实际 %d 条", len(drafts))
	}

	// 模拟运维在后台禁用了这条规则(库里已无启用规则).
	store.rules = nil

	// 前提断言: 未失效时仍按旧快照命中 —— 先把"错的行为"钉住,
	// 否则下面"失效后不命中"的断言可能只是碰巧成立(比如缓存根本没命中过).
	if drafts, _ := e.Evaluate(ctx, engineTempEvent(), "req-2"); len(drafts) != 1 {
		t.Fatalf("前提不成立: 未失效时应仍命中旧快照, 实际 %d 条", len(drafts))
	}

	e.Invalidate()

	drafts, err = e.Evaluate(ctx, engineTempEvent(), "req-3")
	if err != nil {
		t.Fatalf("evaluate after invalidate: %v", err)
	}
	if len(drafts) != 0 {
		t.Errorf("Invalidate 后应按新规则判定为不命中, 实际仍命中 %d 条", len(drafts))
	}
	if e.HasRules(ctx) {
		t.Error("Invalidate 后 HasRules 应为 false: 规则已禁用, 否则消费链路不会走回退/丢弃分支")
	}
}

// TestEngine_InvalidateDoesNotPreload 失效是惰性的: 只清缓存, 不立即回源.
// 连续改 N 条规则时若每次都立即重载, 会退化成 N 次全表查询.
func TestEngine_InvalidateDoesNotPreload(t *testing.T) {
	store := &fakeStore{rules: []Rule{engineTempRule(t, 1)}}
	e := NewEngine(store, newFakeWindow(), time.Minute)
	ctx := context.Background()

	if _, err := e.Evaluate(ctx, engineTempEvent(), "req-1"); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	before := store.calls

	e.Invalidate()
	e.Invalidate() // 模拟连续改两条规则

	if store.calls != before {
		t.Errorf("Invalidate 不应触发查库: 调用次数 %d -> %d", before, store.calls)
	}
}

// TestEngine_InvalidateConcurrentSafe 失效与评估并发时不得 panic / 死锁 / 数据竞争(需配合 -race).
func TestEngine_InvalidateConcurrentSafe(t *testing.T) {
	e := NewEngine(&lockedStore{rules: []Rule{engineTempRule(t, 1)}}, newFakeWindow(), time.Minute)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Evaluate(ctx, engineTempEvent(), "req")
			e.Invalidate()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = e.HasRules(ctx)
	}()
	wg.Wait()
}
