package notify

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dispatch"
)

// decodeLast 解析最后一次发布的载荷为通用 map, 便于按字段名断言对外契约.
func decodeLast(t *testing.T, pub *fakePublisher) map[string]any {
	t.Helper()
	if len(pub.calls) == 0 {
		t.Fatal("没有产生任何发布调用")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(pub.calls[len(pub.calls)-1].value), &got); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v", err)
	}
	return got
}

// TestPublish_FillsPriorityFromSeverity 未按对外暴露 priority 时, 必须由发布器按等级换算补齐.
//
// 为什么要有这条: priority 是 M5 建单时直接使用的字段, 一旦遗漏, 消费方拿到 0
// (不在 1/2/3 合法取值内) —— 要么被 validPriority 拒单(整条工单丢), 要么被当成"普通"。
// 两者都不会报错, 属于典型的静默降级, 只能靠这条契约用例守住.
func TestPublish_FillsPriorityFromSeverity(t *testing.T) {
	cases := map[string]struct {
		severity int8
		want     int8
	}{
		"紧急": {severity: 4, want: dispatch.PriorityUrgent},
		"严重": {severity: 3, want: dispatch.PriorityHigh},
		"一般": {severity: 2, want: dispatch.PriorityNormal},
		"提示": {severity: 1, want: dispatch.PriorityNormal},
		"未知等级": {severity: 9, want: dispatch.PriorityNormal},
	}
	for name, c := range cases {
		pub := &fakePublisher{}
		n := newTestNotifier(pub, "")
		ev := sampleEvent()
		ev.Severity = c.severity

		if err := n.AlarmResolved(context.Background(), ev); err != nil {
			t.Fatalf("%s: 发布应成功: %v", name, err)
		}
		got := decodeLast(t, pub)
		if got["priority"] != float64(c.want) {
			t.Errorf("%s: severity=%d 应换算为 priority=%d, 实际 %v", name, c.severity, c.want, got["priority"])
		}
	}
}

// TestPublish_ExplicitPriorityWins 调用方显式指定了优先级时不得被覆盖.
func TestPublish_ExplicitPriorityWins(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")
	ev := sampleEvent()
	ev.Severity = 1 // 默认会换算为普通(3)
	ev.Priority = dispatch.PriorityUrgent

	if err := n.AlarmCreated(context.Background(), ev); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if got := decodeLast(t, pub)["priority"]; got != float64(dispatch.PriorityUrgent) {
		t.Errorf("显式指定的优先级应保留, 实际 %v", got)
	}
}

// TestPublish_WithPriorityTable 运营配置的覆盖必须真正生效(区别于默认映射).
func TestPublish_WithPriorityTable(t *testing.T) {
	table, _ := dispatch.ParseTable(map[string]int{"3": 1})

	pub := &fakePublisher{}
	n := NewKafkaNotifier(pub, "", WithPriorityTable(table))
	n.backoff = []time.Duration{time.Millisecond, time.Millisecond} // 缩短退避, 避免用例被真实退避拖慢
	ev := sampleEvent()
	ev.Severity = 3 // 默认=高(2), 配置覆盖后应为紧急(1)

	if err := n.AlarmResolved(context.Background(), ev); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if got := decodeLast(t, pub)["priority"]; got != float64(dispatch.PriorityUrgent) {
		t.Errorf("配置覆盖后 severity=3 应为 %d, 实际 %v", dispatch.PriorityUrgent, got)
	}
}

// TestCreatedAndResolvedCarrySamePriority 同一告警的产生与解决事件必须带同一优先级:
// M5 若发现前后不一致, 只能二选一更新工单优先级, 会出现"建单时紧急、关单时变普通"的抖动.
func TestCreatedAndResolvedCarrySamePriority(t *testing.T) {
	pub := &fakePublisher{}
	n := newTestNotifier(pub, "")

	created := sampleEvent()
	created.Status = 0
	resolved := sampleEvent()
	resolved.Status = 2

	if err := n.AlarmCreated(context.Background(), created); err != nil {
		t.Fatalf("发布产生事件失败: %v", err)
	}
	if err := n.AlarmResolved(context.Background(), resolved); err != nil {
		t.Fatalf("发布解决事件失败: %v", err)
	}
	if len(pub.calls) != 2 {
		t.Fatalf("应发布 2 次, 实际 %d 次", len(pub.calls))
	}

	var first, second map[string]any
	if err := json.Unmarshal([]byte(pub.calls[0].value), &first); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(pub.calls[1].value), &second); err != nil {
		t.Fatalf("载荷应为合法 JSON: %v", err)
	}
	if first["priority"] != second["priority"] {
		t.Errorf("两类事件的优先级应一致: create=%v resolve=%v", first["priority"], second["priority"])
	}
	if first["action"] != ActionCreated || second["action"] != ActionResolved {
		t.Errorf("action 顺序应为 %s → %s, 实际 %v → %v",
			ActionCreated, ActionResolved, first["action"], second["action"])
	}
}
