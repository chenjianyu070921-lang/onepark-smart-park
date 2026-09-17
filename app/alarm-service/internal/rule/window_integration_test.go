package rule

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"onepark/common/redisx"
)

// 集成用例: 依赖真实 Redis, 未设置 ALARM_TEST_REDIS_ADDR 时 Skip, 不影响常规 go test.
//
//	ALARM_TEST_REDIS_ADDR='127.0.0.1:6379'
//
// 为什么必须补这一层: 滑动窗口依赖一段 Lua 脚本, 而此前它只在单测替身下被"假装执行"过 ——
// Lua 语法错误、与 Redis 版本不兼容、返回值类型不符, 都只会在线上第一次触发规则时炸。
func realRedis(t *testing.T) *redisx.Client {
	t.Helper()
	addr := os.Getenv("ALARM_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过 Redis 集成用例")
	}
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	return rdb
}

// uniqueWindowKey 生成唯一窗口键, 避免与本机其它用例/实例串扰.
func uniqueWindowKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("alarm:window:test:%d", time.Now().UnixNano())
}

// TestIntegration_SlidingWindowTrigger 达到阈值触发, 且触发后窗口被清空重新计数.
func TestIntegration_SlidingWindowTrigger(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	w := NewRedisWindow(rdb)
	key := uniqueWindowKey(t)

	const threshold = 3
	window := time.Second
	ttl := time.Minute

	// 未达阈值的两次: 不应触发(文档约定未触发返回计数 0).
	for i := 1; i <= threshold-1; i++ {
		triggered, cnt, err := w.Add(ctx, key, fmt.Sprintf("m%d", i), window, threshold, ttl)
		if err != nil {
			t.Fatalf("第 %d 次 Add 失败: %v", i, err)
		}
		if triggered {
			t.Fatalf("第 %d 次不应触发(阈值 %d)", i, threshold)
		}
		if cnt != 0 {
			t.Errorf("未触发时计数应为 0, 实际 %d", cnt)
		}
	}

	triggered, cnt, err := w.Add(ctx, key, "m3", window, threshold, ttl)
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if !triggered {
		t.Fatal("窗口内达到阈值必须触发")
	}
	if cnt != threshold {
		t.Errorf("触发时计数应为 %d, 实际 %d", threshold, cnt)
	}

	// 触发后窗口必须被清空: 否则下一次事件会立刻再次触发, 变成持续刷屏.
	exists, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("Exists 失败: %v", err)
	}
	if exists != 0 {
		t.Error("触发后窗口应被清空(脚本 DEL 未生效)")
	}
	if triggered, _, err := w.Add(ctx, key, "m4", window, threshold, ttl); err != nil {
		t.Fatalf("Add 失败: %v", err)
	} else if triggered {
		t.Error("窗口清空后不应立即再次触发")
	}
}

// TestIntegration_SlidingWindowMemberDedup 同一成员重复投递不得重复计数.
// Kafka 是 at-least-once, 同一 request_id 必然会被重放 —— 若按次数累计, 单条消息重放 3 次
// 就能凭空造出一条"窗口告警".
func TestIntegration_SlidingWindowMemberDedup(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	w := NewRedisWindow(rdb)
	key := uniqueWindowKey(t)

	const threshold = 2
	window := time.Minute
	ttl := 2 * time.Minute

	for i := 0; i < 3; i++ {
		triggered, _, err := w.Add(ctx, key, "same-request-id", window, threshold, ttl)
		if err != nil {
			t.Fatalf("第 %d 次 Add 失败: %v", i+1, err)
		}
		if triggered {
			t.Fatal("同一 member 重复投递不应累计计数(必须按成员去重)")
		}
	}

	// 换一个成员才应达到阈值, 且计数为 2(说明前一条只算了一次).
	triggered, cnt, err := w.Add(ctx, key, "another-request-id", window, threshold, ttl)
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if !triggered || cnt != threshold {
		t.Errorf("期望触发且计数为 %d, 实际 triggered=%v cnt=%d", threshold, triggered, cnt)
	}
}

// TestIntegration_SlidingWindowAgesOut 滑出窗口的事件必须被清理(ZREMRANGEBYSCORE 生效).
func TestIntegration_SlidingWindowAgesOut(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	w := NewRedisWindow(rdb)
	key := uniqueWindowKey(t)

	window := 300 * time.Millisecond
	const threshold = 2
	ttl := time.Minute

	if triggered, _, err := w.Add(ctx, key, "old", window, threshold, ttl); err != nil {
		t.Fatalf("Add 失败: %v", err)
	} else if triggered {
		t.Fatal("首次不应触发")
	}

	// 等旧成员滑出窗口: 此时窗口内应只剩新成员, 不足阈值.
	time.Sleep(400 * time.Millisecond)
	triggered, cnt, err := w.Add(ctx, key, "new", window, threshold, ttl)
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if triggered {
		t.Errorf("旧成员应已滑出窗口, 不应触发(实际 cnt=%d)", cnt)
	}
}

// TestIntegration_SlidingWindowKeyTTL 窗口键必须带 TTL:
// 否则低频规则的键会永久堆积(每条规则×每个设备一个键), 逐步把 Redis 写满.
func TestIntegration_SlidingWindowKeyTTL(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	w := NewRedisWindow(rdb)
	key := uniqueWindowKey(t)

	// 阈值给大值, 避免触发时 DEL 掉键导致读不到 TTL.
	if _, _, err := w.Add(ctx, key, "m", time.Second, 100, 2*time.Second); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL 查询失败: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("窗口键必须设置过期时间, 实际 TTL=%s(<=0 表示永不过期或键不存在)", ttl)
	}
}

// TestIntegration_SlidingWindowRedisDown 真实连接失败必须返回 error(调用方据此重投).
func TestIntegration_SlidingWindowRedisDown(t *testing.T) {
	if os.Getenv("ALARM_TEST_REDIS_ADDR") == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过 Redis 集成用例")
	}
	dead := goredis.NewClient(&goredis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1, // -1 = 不重试
	})
	defer func() { _ = dead.Close() }()

	if _, _, err := NewRedisWindow(dead).Add(
		context.Background(), "k", "m", time.Second, 1, time.Minute); err == nil {
		t.Error("Redis 不可用时 Add 必须返回错误(否则窗口规则会静默失效)")
	}
}

// TestIntegration_EngineTimeWindowWithRealRedis 端到端: 规则引擎 → 真实 Redis Lua 滑动窗口.
// 单测里窗口是 fake, Lua 从未真正执行过; 这里让引擎走完整真实路径,
// 验证"同设备 5 分钟内 3 次闯入才告警"这条规则在真机上成立。
func TestIntegration_EngineTimeWindowWithRealRedis(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()

	// 规则ID 取纳秒时间戳: 窗口键含规则ID, 唯一化可避免与其它用例串扰.
	ruleID := time.Now().UnixNano()
	const deviceID = "door-e2e"
	store := &fakeStore{rules: []Rule{{
		ID: ruleID, Name: "5分钟内3次闯入", EventType: "intrusion", Level: 4,
		Spec: mustSpec(t,
			`{"type":"time_window","window_sec":300,"threshold":3,"match":{"field":"event_type","op":"eq","value":"intrusion"}}`),
	}}}
	engine := NewEngine(store, NewRedisWindow(rdb), time.Minute)
	f := Fields{EventType: "intrusion", DeviceID: deviceID}

	for i := 1; i <= 2; i++ {
		got, err := engine.Evaluate(ctx, f, fmt.Sprintf("rid-e2e-%d", i))
		if err != nil {
			t.Fatalf("第 %d 次求值出错: %v", i, err)
		}
		if len(got) != 0 {
			t.Fatalf("第 %d 次未达阈值不应触发: %+v", i, got)
		}
	}

	got, err := engine.Evaluate(ctx, f, "rid-e2e-3")
	if err != nil {
		t.Fatalf("求值出错: %v", err)
	}
	if len(got) != 1 || got[0].RuleID != ruleID {
		t.Fatalf("第 3 次达到阈值应触发: %+v", got)
	}

	// 触发后窗口清空: 第 4 次重新计数, 不应立刻再次触发(否则会持续刷屏).
	if got, err = engine.Evaluate(ctx, f, "rid-e2e-4"); err != nil {
		t.Fatalf("求值出错: %v", err)
	} else if len(got) != 0 {
		t.Errorf("窗口已清空, 第 4 次不应触发: %+v", got)
	}

	// 窗口键必须真实落在 Redis 上, 且键名与 docs/m3/08 §1 约定一致.
	key := WindowKey(ruleID, deviceID)
	exists, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("Exists 失败: %v", err)
	}
	if exists != 1 {
		t.Errorf("滑动窗口键应存在于 Redis: %s", key)
	}
}
