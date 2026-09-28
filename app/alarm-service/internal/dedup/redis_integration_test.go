package dedup

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
// 为什么必须补这一层: L1 幂等与 L2 冷却此前只有单测替身覆盖 —— 替身复刻的是"我以为的 SetNX 语义",
// 真实 Redis 上的 TTL 是否真的生效、连接失败是否真的返回 error, 从未被验证过。
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

// uniqueKey 生成带纳秒时间戳的唯一键, 避免与本机其它用例/实例串扰.
func uniqueKey(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("alarm:test:%s:%d", prefix, time.Now().UnixNano())
}

// TestIntegration_RedisDeduper L1 幂等: 首次放行、重复拦截、TTL 到期后重新放行.
func TestIntegration_RedisDeduper(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	d := NewRedisDeduper(rdb, 300*time.Millisecond)
	key := uniqueKey(t, "dedup")

	seen, err := d.Seen(ctx, key)
	if err != nil {
		t.Fatalf("首次 Seen 不应报错: %v", err)
	}
	if seen {
		t.Fatal("首次消息不应被判为重复")
	}

	seen, err = d.Seen(ctx, key)
	if err != nil {
		t.Fatalf("重复 Seen 不应报错: %v", err)
	}
	if !seen {
		t.Fatal("同一 request_id 第二次必须判为重复(P0-5 核心语义)")
	}

	// 幂等键必须带 TTL: 否则键永久驻留, Redis 会被逐条告警慢慢写满.
	// 必须用 PTTL 而非 TTL —— Redis 的 TTL 以秒为粒度向下取整, 亚秒级 TTL 会被报成 0,
	// 用它断言会得出"没设过期时间"的错误结论(P 开头才是毫秒精度).
	pttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL 查询失败: %v", err)
	}
	if pttl <= 0 {
		t.Errorf("幂等键必须设置过期时间, 实际 PTTL=%s(<=0 表示永不过期或键不存在)", pttl)
	}
	if pttl > 300*time.Millisecond {
		t.Errorf("幂等键 TTL 超出配置值: %s", pttl)
	}

	// TTL 到期后同一 request_id 应可重新处理(幂等窗口结束).
	time.Sleep(400 * time.Millisecond)
	seen, err = d.Seen(ctx, key)
	if err != nil {
		t.Fatalf("过期后 Seen 不应报错: %v", err)
	}
	if seen {
		t.Error("幂等窗口过期后应允许重新处理")
	}
}

// TestIntegration_RedisDeduperRelease 释放语义: 释放后同一 key 必须能被重新处理.
//
// 这条直接对准远程开门的真实依赖: 命令下发失败/被拒时释放幂等键, 调用方带同一 request_id
// 重试才能重新走到命令下发, 而不是拿到一个"门没开却报成功"的响应.
func TestIntegration_RedisDeduperRelease(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	d := NewRedisDeduper(rdb, time.Minute)
	key := uniqueKey(t, "dedup-release")

	// 先预占.
	if seen, err := d.Seen(ctx, key); err != nil || seen {
		t.Fatalf("首次 Seen 应放行且不报错: seen=%v err=%v", seen, err)
	}
	if seen, err := d.Seen(ctx, key); err != nil || !seen {
		t.Fatalf("预占后必须判为重复: seen=%v err=%v", seen, err)
	}

	// 释放后必须重新可处理.
	if err := d.Release(ctx, key); err != nil {
		t.Fatalf("Release 不应报错: %v", err)
	}
	pttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL 查询失败: %v", err)
	}
	// Redis 约定: PTTL 返回 -2 表示键不存在, -1 表示存在但无过期时间.
	if pttl != -2*time.Nanosecond {
		t.Errorf("Release 后键应已删除(PTTL=-2), 实际 PTTL=%s", pttl)
	}
	if seen, err := d.Seen(ctx, key); err != nil || seen {
		t.Errorf("释放后同一 key 必须可重新处理: seen=%v err=%v", seen, err)
	}

	// 键不存在时 Release 也必须成功(幂等): 否则"清键失败"会被误判成故障 ——
	// 调用方对清键失败只记日志, 一个假故障只会变成噪声.
	if err := d.Release(ctx, uniqueKey(t, "dedup-absent")); err != nil {
		t.Errorf("释放不存在的键不应报错: %v", err)
	}
}

// TestIntegration_RedisCooldown L2 冷却: 窗口内抑制、窗口过期后重新放行.
func TestIntegration_RedisCooldown(t *testing.T) {
	rdb := realRedis(t)
	ctx := context.Background()
	c := NewRedisCooldown(rdb)
	key := uniqueKey(t, "cooldown")

	cooling, err := c.TryAcquire(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("首次 TryAcquire 不应报错: %v", err)
	}
	if cooling {
		t.Fatal("首次事件不应处于冷却中")
	}

	cooling, err = c.TryAcquire(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("冷却期内 TryAcquire 不应报错: %v", err)
	}
	if !cooling {
		t.Fatal("冷却窗口内的同设备同事件必须被抑制")
	}

	// 同 L1: 用 PTTL 规避秒级取整(见上).
	pttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL 查询失败: %v", err)
	}
	if pttl <= 0 {
		t.Errorf("冷却键必须设置过期时间, 实际 PTTL=%s —— 无 TTL 会让抑制变成永久静音", pttl)
	}
	if pttl > 300*time.Millisecond {
		t.Errorf("冷却键 TTL 超出配置值: %s", pttl)
	}

	// 窗口过期后必须重新放行: 冷却退化不得成为"设备永久静音".
	time.Sleep(400 * time.Millisecond)
	cooling, err = c.TryAcquire(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("过期后 TryAcquire 不应报错: %v", err)
	}
	if cooling {
		t.Error("冷却窗口过期后应重新放行")
	}
}

// TestIntegration_RedisUnavailableReturnsError 真实连接失败必须返回 error.
// 调用方据此重投而非降级放行(docs/m3/08 §5: 宁积压不误报) —— 这条契约此前只靠替身"约定"。
func TestIntegration_RedisUnavailableReturnsError(t *testing.T) {
	if os.Getenv("ALARM_TEST_REDIS_ADDR") == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过 Redis 集成用例")
	}
	// 指向必然无人监听的端口, 并压短超时/关闭重试: 断言的是错误语义, 不是等待时长.
	dead := goredis.NewClient(&goredis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1, // -1 = 不重试
	})
	defer func() { _ = dead.Close() }()
	ctx := context.Background()

	if _, err := NewRedisDeduper(dead, time.Minute).Seen(ctx, "k"); err == nil {
		t.Error("Redis 不可用时 Seen 必须返回错误(否则重复消息会被静默放行)")
	}
	if _, err := NewRedisCooldown(dead).TryAcquire(ctx, "k", time.Minute); err == nil {
		t.Error("Redis 不可用时 TryAcquire 必须返回错误")
	}
}
