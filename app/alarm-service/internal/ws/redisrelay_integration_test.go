package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// 集成用例: 依赖真实 Redis, 未设置 ALARM_TEST_REDIS_ADDR 时 Skip, 不影响常规 go test.
//
//	ALARM_TEST_REDIS_ADDR='127.0.0.1:6379'
//
// 对应 docs/m3/09 §7 的验收项"多实例(起两个进程 + Redis Pub/Sub)验证跨实例可达"。
// 这里用两个独立 Hub + 两个独立 Redis 客户端模拟两个实例 —— 对 Pub/Sub 扇形分发而言,
// 与两个进程等价(订阅者各自独立), 但无需真实起两个进程与两份配置。
func TestIntegration_RedisRelayCrossInstance(t *testing.T) {
	addr := os.Getenv("ALARM_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过 Redis 跨实例集成用例")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 频道带时间戳: 避免与同机其它用例/实例串扰.
	channel := fmt.Sprintf("alarm:ws:broadcast:test:%d", time.Now().UnixNano())

	clientA := goredis.NewClient(&goredis.Options{Addr: addr})
	clientB := goredis.NewClient(&goredis.Options{Addr: addr})
	defer func() { _ = clientA.Close() }()
	defer func() { _ = clientB.Close() }()

	hubA := NewHub() // 实例A: 消费 Kafka 后产生告警
	hubB := NewHub() // 实例B: 大屏连在这里
	hubA.StartRelay(ctx, NewRedisRelay(clientA, channel))
	hubB.StartRelay(ctx, NewRedisRelay(clientB, channel))

	// 必须等到订阅真正建立再发消息: SUBSCRIBE 是异步的, 抢跑会因"无人订阅"而丢消息,
	// 让后续断言变成假阴性.
	waitSubscribers(t, clientA, channel, 2, 5*time.Second)

	cB := addClient(t, hubB, 1)     // 实例B 上园区1的大屏
	otherTenant := addClient(t, hubB, 2) // 实例B 上园区2的大屏
	cA := addClient(t, hubA, 1)     // 实例A 自己也有园区1的大屏

	hubA.Push(1, NewEnvelope(TypeAlarmCreated, AlarmEvent{AlarmID: "AL-20260922-0100", DeviceID: "door-01", Level: 3}, "rid-x"))

	// 核心断言: 实例B 的客户端必须收到实例A 产生的告警.
	msg, ok := recv(cB, 3*time.Second)
	if !ok {
		t.Fatal("跨实例广播未送达: 实例B 未收到实例A 产生的告警")
	}
	var env Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("推送消息非法: %v", err)
	}
	if env.Type != TypeAlarmCreated || env.RequestId != "rid-x" {
		t.Errorf("信封内容异常: %+v", env)
	}
	data, ok := env.Data.(map[string]interface{})
	if !ok || data["device_id"] != "door-01" {
		t.Errorf("载荷内容异常: %+v", env.Data)
	}

	// 本实例的客户端经 Redis 回声收到(投递路径与跨实例一致, 且不会重复).
	if _, ok := recv(cA, 3*time.Second); !ok {
		t.Error("实例A 自己的客户端应经回声收到告警")
	}
	// 园区隔离在跨实例路径上同样成立.
	if msg, ok := recv(otherTenant, 200*time.Millisecond); ok {
		t.Errorf("园区2不应收到园区1的告警: %s", msg)
	}

	// 记录 Pub/Sub 的已知取舍(docs/m3/09 §4): 无持久化, 订阅建立前发布的消息会丢,
	// 由前端重连后拉活跃告警列表补偿. 断言它确实收不到历史消息.
	clientC := goredis.NewClient(&goredis.Options{Addr: addr})
	defer func() { _ = clientC.Close() }()
	hubC := NewHub()
	hubC.StartRelay(ctx, NewRedisRelay(clientC, channel))
	late := addClient(t, hubC, 1)
	waitSubscribers(t, clientA, channel, 3, 5*time.Second)

	if msg, ok := recv(late, 300*time.Millisecond); ok {
		t.Errorf("Pub/Sub 不应回放历史消息(否则说明实现有误): %s", msg)
	}
	// 但订阅建立之后的消息必须能收到.
	hubA.Push(1, NewEnvelope(TypeAlarmResolved, AlarmEvent{AlarmID: "AL-20260922-0100", Status: 2}, "rid-y"))
	if _, ok := recv(late, 3*time.Second); !ok {
		t.Error("订阅建立后的新消息应送达")
	}
}

// waitSubscribers 轮询 PUBSUB NUMSUB 直到频道订阅数达到期望值.
// 不用 sleep 硬等: 订阅未就绪时的失败是偶发假阴性, 会让用例变得不可信.
func waitSubscribers(t *testing.T, client *goredis.Client, channel string, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		counts, err := client.PubSubNumSub(context.Background(), channel).Result()
		if err == nil {
			last = counts[channel]
			if last >= want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 %d 个订阅者超时(当前 %d): 订阅未就绪会让后续断言产生假阴性", want, last)
}
