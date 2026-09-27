package rule

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type fakeStore struct {
	rules []Rule
	err   error
	calls int
}

func (f *fakeStore) ListEnabled(context.Context) ([]Rule, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.rules, nil
}

// fakeWindow 内存版滑动窗口: 记录 member 去重后的窗口内计数.
type fakeWindow struct {
	counts map[string]int
	err    error
}

func newFakeWindow() *fakeWindow { return &fakeWindow{counts: map[string]int{}} }

func (f *fakeWindow) Add(_ context.Context, key, member string, _ time.Duration, threshold int, _ time.Duration) (bool, int64, error) {
	if f.err != nil {
		return false, 0, f.err
	}
	if f.counts[key+":"+member] > 0 {
		// member 已存在: ZADD 为更新, 计数不变(验证 member 去重语义).
		return false, int64(f.counts[key]), nil
	}
	f.counts[key]++
	f.counts[key+":"+member]++
	return f.counts[key] >= threshold, int64(f.counts[key]), nil
}

func mustSpec(t *testing.T, raw string) *NormalizedSpec {
	t.Helper()
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("解析规则条件失败: %v", err)
	}
	return spec
}

// ---------------------------------------------------------------------------
// 条件解析与算子
// ---------------------------------------------------------------------------

func TestParseSpec_CompatBothDocFormats(t *testing.T) {
	// docs/m3/07 的嵌套写法.
	nested := mustSpec(t, `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gte","value":80}]}`)
	if nested.Type != RuleTypeThreshold || len(nested.Conditions) != 1 {
		t.Errorf("嵌套写法解析异常: %+v", nested)
	}
	// docs/m3/04 #34 的扁平写法.
	flat := mustSpec(t, `{"field":"temperature","operator":">","value":50}`)
	if len(flat.Conditions) != 1 || flat.Conditions[0].Field != "temperature" {
		t.Errorf("扁平写法解析异常: %+v", flat)
	}
	// 文档 04/07 的类型名差异: composite/window 需归一化.
	if mustSpec(t, `{"type":"composite","conditions":[{"field":"event_type","op":"eq","value":"x"}]}`).Type != RuleTypeCombination {
		t.Error("composite 应归一化为 combination")
	}
	if mustSpec(t, `{"type":"window","window_sec":60,"threshold":3}`).Type != RuleTypeTimeWindow {
		t.Error("window 应归一化为 time_window")
	}
}

func TestParseSpec_Invalid(t *testing.T) {
	bad := []string{
		``,                                                                 // 空
		`not-json`,                                                         // 非法 JSON
		`{"type":"threshold","conditions":[{"op":"eq","value":1}]}`,        // 缺 field
		`{"type":"threshold","conditions":[{"field":"a","op":">><","value":1}]}`, // 非法算子
		`{"type":"threshold","logic":"XOR","conditions":[{"field":"a","op":"eq","value":1}]}`,
	}
	for _, raw := range bad {
		if _, err := ParseSpec(raw); err == nil {
			t.Errorf("非法条件应报错: %s", raw)
		}
	}
}

func TestEvaluateCondition_Operators(t *testing.T) {
	f := Fields{EventType: "temperature", DeviceID: "dev-1", AreaID: 12, TenantID: 1,
		Payload: map[string]interface{}{"temperature": 85.5, "door_status": "open", "level": 3}}

	cases := []struct {
		name  string
		field string
		op    string
		value interface{}
		want  bool
	}{
		{"gte 命中", "payload.temperature", "gte", 80, true},
		{"gte 未命中", "payload.temperature", "gte", 90, false},
		{"符号算子 >", "payload.temperature", ">", 80, true},
		{"lt", "payload.temperature", "lt", 90, true},
		{"eq 字符串", "payload.door_status", "eq", "open", true},
		{"ne 字符串", "payload.door_status", "ne", "closed", true},
		{"eq 数值", "payload.level", "eq", 3, true},
		{"in 命中", "payload.door_status", "in", []interface{}{"open", "ajar"}, true},
		{"in 未命中", "payload.door_status", "in", []interface{}{"closed"}, false},
		{"in 逗号串", "payload.door_status", "in", "closed,open", true},
		{"between 命中", "payload.temperature", "between", []interface{}{80, 90}, true},
		{"between 未命中", "payload.temperature", "between", []interface{}{90, 100}, false},
		{"字段缺失视为未命中", "payload.humidity", "gte", 10, false},
		{"不支持的路径", "foo.bar", "eq", 1, false},
		{"event_type 取值", "event_type", "eq", "temperature", true},
		{"device_id 取值", "device_id", "eq", "dev-1", true},
	}
	for _, c := range cases {
		got, err := EvaluateCondition(f, Condition{Field: c.field, Op: c.op, Value: c.value})
		if err != nil {
			t.Errorf("%s: 求值出错: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: 期望 %v, 实际 %v", c.name, c.want, got)
		}
	}
}

func TestEvaluateCondition_InvalidOperator(t *testing.T) {
	if _, err := EvaluateCondition(Fields{}, Condition{Field: "payload.x", Op: "~~", Value: 1}); err == nil {
		t.Error("非法操作符应报错")
	}
}

// ---------------------------------------------------------------------------
// 引擎: 阈值 / 组合 / 时间窗口
// ---------------------------------------------------------------------------

func TestEngine_ThresholdHit(t *testing.T) {
	store := &fakeStore{rules: []Rule{{
		ID: 1001, Name: "温度越限", EventType: "temperature", Level: 3,
		Spec: mustSpec(t, `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gte","value":80}]}`),
	}}}
	engine := NewEngine(store, newFakeWindow(), time.Minute)
	f := Fields{EventType: "temperature", DeviceID: "dev-1", Payload: map[string]interface{}{"temperature": 85}}

	got, err := engine.Evaluate(context.Background(), f, "rid-1")
	if err != nil {
		t.Fatalf("求值出错: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != 1001 || got[0].Level != 3 {
		t.Fatalf("阈值规则未命中: %+v", got)
	}
}

func TestEngine_ThresholdNotHit(t *testing.T) {
	store := &fakeStore{rules: []Rule{{
		ID: 1002, Name: "温度越限", EventType: "temperature", Level: 3,
		Spec: mustSpec(t, `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gte","value":80}]}`),
	}}}
	engine := NewEngine(store, newFakeWindow(), time.Minute)
	f := Fields{EventType: "temperature", DeviceID: "dev-1", Payload: map[string]interface{}{"temperature": 30}}

	got, err := engine.Evaluate(context.Background(), f, "rid-1")
	if err != nil || len(got) != 0 {
		t.Fatalf("未达阈值不应产出草稿: %+v err=%v", got, err)
	}
}

// TestEngine_ScopeFilter 规则的 eventType/device/area 三维范围过滤.
func TestEngine_ScopeFilter(t *testing.T) {
	spec := mustSpec(t, `{"type":"threshold","conditions":[{"field":"payload.x","op":"eq","value":1}]}`)
	store := &fakeStore{rules: []Rule{
		{ID: 1, Name: "限定设备", DeviceID: "dev-9", Spec: spec},
		{ID: 2, Name: "限定区域", AreaID: 77, Spec: spec},
		{ID: 3, Name: "* 通配设备", DeviceID: "*", Spec: spec},
	}}
	engine := NewEngine(store, newFakeWindow(), time.Minute)
	f := Fields{DeviceID: "dev-1", AreaID: 12, Payload: map[string]interface{}{"x": 1}}

	got, err := engine.Evaluate(context.Background(), f, "rid")
	if err != nil {
		t.Fatalf("求值出错: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != 3 {
		t.Errorf("仅通配规则应命中, 实际: %+v", got)
	}
}

// TestRule_AppliesToDeviceType 规则适用范围新增设备类型维度(「设备类型 + 事件类型 → 等级」的前提).
//
// 其中"摄像头上报 intrusion 不应命中门禁规则"是本次改动的核心诉求:
// 规则只写 event_type=intrusion 时, 任何设备上报的闯入都会被当成门禁闯入。
func TestRule_AppliesToDeviceType(t *testing.T) {
	doorIntrusion := Fields{EventType: "intrusion", DeviceID: "door-1", DeviceType: "access_control", AreaID: 3}
	camIntrusion := Fields{EventType: "intrusion", DeviceID: "cam-1", DeviceType: "camera", AreaID: 3}
	// M1 的 device_type 是 optional 字段, 缺字段的事件只能靠 event_type 判定.
	noTypeIntrusion := Fields{EventType: "intrusion", DeviceID: "door-2"}

	cases := []struct {
		name string
		rule Rule
		f    Fields
		want bool
	}{
		{"门禁规则命中门禁事件", Rule{DeviceType: "access_control"}, doorIntrusion, true},
		{"门禁规则不命中摄像头事件", Rule{DeviceType: "access_control"}, camIntrusion, false},
		{"不限设备类型的规则仍全命中", Rule{}, camIntrusion, true},
		{"事件未上报设备类型时放行", Rule{DeviceType: "access_control"}, noTypeIntrusion, true},
		{"设备类型+事件类型同时限定", Rule{DeviceType: "access_control", EventType: "intrusion"}, doorIntrusion, true},
		{"事件类型不符不命中", Rule{DeviceType: "access_control", EventType: "door_forced"}, doorIntrusion, false},
		{"设备ID维度仍独立生效", Rule{DeviceType: "access_control", DeviceID: "door-9"}, doorIntrusion, false},
	}
	for _, c := range cases {
		if got := c.rule.AppliesTo(c.f); got != c.want {
			t.Errorf("%s: 期望 %v, 实际 %v", c.name, c.want, got)
		}
	}
}

// TestEngine_DeviceTypeScopedRule 引擎按设备类型过滤规则: 摄像头上报不被门禁规则命中.
func TestEngine_DeviceTypeScopedRule(t *testing.T) {
	store := &fakeStore{rules: []Rule{{
		ID: 1, Name: "门禁非法闯入告警", DeviceType: "access_control", EventType: "intrusion", Level: 2,
		Spec: mustSpec(t, `{"type":"threshold","field":"event_type","op":"eq","value":"intrusion"}`),
	}}}
	engine := NewEngine(store, newFakeWindow(), time.Minute)
	ctx := context.Background()

	got, err := engine.Evaluate(ctx, Fields{EventType: "intrusion", DeviceID: "door-1", DeviceType: "access_control"}, "rid-1")
	if err != nil {
		t.Fatalf("求值出错: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != 1 {
		t.Fatalf("门禁设备上报的 intrusion 应命中门禁规则: %+v", got)
	}

	got, err = engine.Evaluate(ctx, Fields{EventType: "intrusion", DeviceID: "cam-1", DeviceType: "camera"}, "rid-2")
	if err != nil {
		t.Fatalf("求值出错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("摄像头上报的 intrusion 不应命中门禁规则, 实际: %+v", got)
	}
}

// TestEngine_CombinationLogic AND/OR 两种组合语义.
func TestEngine_CombinationLogic(t *testing.T) {
	base := Fields{DeviceID: "dev-1", EventType: "door",
		Payload: map[string]interface{}{"door_status": "open", "auth_result": "fail"}}

	andStore := &fakeStore{rules: []Rule{{ID: 10, Name: "门磁开且未授权", EventType: "door", Level: 4,
		Spec: mustSpec(t, `{"type":"combination","logic":"AND","conditions":[
			{"field":"payload.door_status","op":"eq","value":"open"},
			{"field":"payload.auth_result","op":"eq","value":"fail"}]}`)}}}
	orStore := &fakeStore{rules: []Rule{{ID: 11, Name: "门磁开或无授权", EventType: "door", Level: 3,
		Spec: mustSpec(t, `{"type":"combination","logic":"OR","conditions":[
			{"field":"payload.door_status","op":"eq","value":"closed"},
			{"field":"payload.auth_result","op":"eq","value":"fail"}]}`)}}}

	if got, _ := NewEngine(andStore, newFakeWindow(), time.Minute).Evaluate(context.Background(), base, "r1"); len(got) != 1 {
		t.Errorf("AND 组合应命中: %+v", got)
	}
	if got, _ := NewEngine(orStore, newFakeWindow(), time.Minute).Evaluate(context.Background(), base, "r2"); len(got) != 1 {
		t.Errorf("OR 组合应命中: %+v", got)
	}

	// AND 有一条不满足即不命中.
	partial := base
	partial.Payload = map[string]interface{}{"door_status": "open", "auth_result": "success"}
	if got, _ := NewEngine(andStore, newFakeWindow(), time.Minute).Evaluate(context.Background(), partial, "r3"); len(got) != 0 {
		t.Errorf("AND 缺一条条件不应命中: %+v", got)
	}
}

// TestEngine_TimeWindowTrigger 滑动窗口达到阈值才触发, 且同 member 不重复计数.
func TestEngine_TimeWindowTrigger(t *testing.T) {
	store := &fakeStore{rules: []Rule{{ID: 20, Name: "5分钟内3次闯入", EventType: "intrusion", Level: 4,
		Spec: mustSpec(t, `{"type":"time_window","window_sec":300,"threshold":3,"match":{"field":"event_type","op":"eq","value":"intrusion"}}`)}}}
	window := newFakeWindow()
	engine := NewEngine(store, window, time.Minute)
	f := Fields{EventType: "intrusion", DeviceID: "door-1"}

	for i := 1; i <= 2; i++ {
		got, err := engine.Evaluate(context.Background(), f, "rid-"+string(rune('0'+i)))
		if err != nil {
			t.Fatalf("第 %d 次求值出错: %v", i, err)
		}
		if len(got) != 0 {
			t.Fatalf("第 %d 次未达阈值不应触发", i)
		}
	}
	// 第 3 次达到阈值.
	got, err := engine.Evaluate(context.Background(), f, "rid-3")
	if err != nil || len(got) != 1 {
		t.Fatalf("第 3 次应触发: %+v err=%v", got, err)
	}
	if got[0].WindowHits != 3 {
		t.Errorf("应带回窗口计数 3, 实际 %d", got[0].WindowHits)
	}

	// 同 request_id 重复投递不重复计数(第 4/5 次用旧 member, 窗口已被清空后重新计数).
	if _, err := engine.Evaluate(context.Background(), f, "rid-3"); err != nil {
		t.Fatalf("重复 member 求值出错: %v", err)
	}
}

// TestEngine_TimeWindowRequiresCounter 窗口计数组件缺失时跳过该规则, 不能退化为逐条触发.
func TestEngine_TimeWindowRequiresCounter(t *testing.T) {
	store := &fakeStore{rules: []Rule{{ID: 21, Name: "窗口规则", EventType: "intrusion", Level: 4,
		Spec: mustSpec(t, `{"type":"time_window","window_sec":60,"threshold":1}`)}}}
	engine := NewEngine(store, nil, time.Minute) // windows == nil

	for i := 0; i < 3; i++ {
		got, err := engine.Evaluate(context.Background(), Fields{EventType: "intrusion", DeviceID: "d"}, "r")
		if err != nil {
			t.Fatalf("求值出错: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("无窗口计数器时应跳过窗口规则, 实际: %+v", got)
		}
	}
}

// TestEngine_WindowErrorPropagates 窗口计数失败必须上抛, 不能静默丢事件.
func TestEngine_WindowErrorPropagates(t *testing.T) {
	store := &fakeStore{rules: []Rule{{ID: 22, Name: "窗口规则", EventType: "intrusion",
		Spec: mustSpec(t, `{"type":"time_window","window_sec":60,"threshold":1}`)}}}
	engine := NewEngine(store, &fakeWindow{err: errors.New("redis down")}, time.Minute)

	if _, err := engine.Evaluate(context.Background(), Fields{EventType: "intrusion", DeviceID: "d"}, "r"); err == nil {
		t.Fatal("窗口计数失败应上抛错误")
	}
}

// TestEngine_CacheAndStoreFailure 规则快照走缓存; 加载失败保留上一次快照.
func TestEngine_CacheAndStoreFailure(t *testing.T) {
	store := &fakeStore{rules: []Rule{{ID: 30, Name: "无条件规则",
		Spec: mustSpec(t, `{"type":"threshold","conditions":[{"field":"event_type","op":"eq","value":"x"}]}`)}}}
	engine := NewEngine(store, newFakeWindow(), time.Minute)
	ctx := context.Background()

	if got, _ := engine.Evaluate(ctx, Fields{EventType: "x"}, "r"); len(got) != 1 {
		t.Fatal("首次评估应命中")
	}
	// 缓存有效期内不应重复查库.
	if _, err := engine.Evaluate(ctx, Fields{EventType: "x"}, "r"); err != nil {
		t.Fatalf("二次评估出错: %v", err)
	}
	if store.calls != 1 {
		t.Errorf("缓存未生效, 查库次数=%d", store.calls)
	}

	// 存储抖动: 错误必须上抛且不能返回基于旧规则的半成品结果(宁可重试, 也不按过期规则误报).
	store.err = errors.New("mysql down")
	engine.cacheTTL = 0 // 0 = 关闭缓存, 强制下次查库(见 snapshot 注释: 不能依赖极短 TTL)
	got, err := engine.Evaluate(ctx, Fields{EventType: "x"}, "r")
	if err == nil {
		t.Error("规则加载失败应返回错误")
	}
	if len(got) != 0 {
		t.Errorf("加载失败不应产出草稿: %+v", got)
	}
	// 存储恢复后应重新生效.
	store.err = nil
	if got, err := engine.Evaluate(ctx, Fields{EventType: "x"}, "r"); err != nil || len(got) != 1 {
		t.Errorf("存储恢复后应重新命中: got=%+v err=%v", got, err)
	}
}

// TestEngine_HasRules 供消费链路判断是否回退硬编码规则.
func TestEngine_HasRules(t *testing.T) {
	empty := NewEngine(&fakeStore{}, newFakeWindow(), time.Minute)
	if empty.HasRules(context.Background()) {
		t.Error("无规则时 HasRules 应为 false")
	}
	with := NewEngine(&fakeStore{rules: []Rule{{ID: 1, Name: "r"}}}, newFakeWindow(), time.Minute)
	if !with.HasRules(context.Background()) {
		t.Error("有规则时 HasRules 应为 true")
	}
	broken := NewEngine(&fakeStore{err: errors.New("down")}, newFakeWindow(), time.Minute)
	if broken.HasRules(context.Background()) {
		t.Error("加载失败时 HasRules 应为 false(交由回退逻辑处理)")
	}
}
