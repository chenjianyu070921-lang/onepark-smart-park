package svc

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/ws"
	"onepark/common/kafka"
	"onepark/common/redisx"

	kafkago "github.com/segmentio/kafka-go"
)

// 真实 Kafka 传输层端到端: M1 遥测 → broker → M3 消费落库 → 告警事件 → broker → M5 消费。
//
// 为什么单独一个文件: intrusion_e2e_test.go 直接驱动 HandleDeviceEvent, 覆盖的是
// "判定/去重/落库/推送" 的真实组件链, 唯独跳过了 Kafka 的传输与位移提交 —— 那一段此前
// 因为本机无 broker 而从未被验证过。本用例把它补齐: 消息真的经过 broker 往返,
// 且用**不同的消费组**分别消费上下游两个 topic, 等价于两个独立部署的服务。
//
// 运行(需先起 broker, 见 deploy/kafka/README.md 方式 A: 双监听器, 宿主机 19092):
//
//	ALARM_TEST_DSN='root:@tcp(127.0.0.1:3399)/alarm_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//	ALARM_TEST_REDIS_ADDR='127.0.0.1:6379'
//	ALARM_TEST_KAFKA_ADDR='127.0.0.1:19092'
//	go test ./internal/svc/ -run TestE2E_KafkaTransport -v
func TestE2E_KafkaTransport(t *testing.T) {
	db := realDB(t) // 未设置 ALARM_TEST_DSN 时自动 Skip
	redisAddr := os.Getenv("ALARM_TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过 Kafka 端到端(需要真实 Redis)")
	}
	brokers := os.Getenv("ALARM_TEST_KAFKA_ADDR")
	if brokers == "" {
		t.Skip("未设置 ALARM_TEST_KAFKA_ADDR, 跳过 Kafka 端到端(需要真实 broker)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// ---- 装配: 与 NewServiceContext 同构, 全部真实组件(含真实 Kafka 通知器) ----
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis 不可用: %v", err)
	}
	rules := model.NewAlarmRuleModel(db)
	producer := kafka.NewProducer(brokers)
	defer func() { _ = producer.Close() }()
	svcCtx := &ServiceContext{
		Alarms:      model.NewAlarmModel(db),
		Rules:       rules,
		Engine:      rule.NewEngine(rules, rule.NewRedisWindow(rdb), 30*time.Second),
		Dedup:       dedup.NewRedisDeduper(rdb, dedupTTL),
		Cooldown:    dedup.NewRedisCooldown(rdb),
		DeadLetters: model.NewDeadLetterModel(db),
		Hub:         ws.NewHub(),
		Redis:       rdb,
		Notifier:    notify.NewKafkaNotifier(producer, notify.DefaultTopic),
	}

	// ---- 下游: 独立消费组订阅 M3 生产的告警事件(等价于 M5 侧消费者) ----
	// 消费组名每次运行都唯一: common/kafka.NewConsumer 固定 StartOffset=FirstOffset,
	// 复用旧组名会被历史位移干扰(表现为"收不到本次事件")。
	var mu sync.Mutex
	var received []notify.AlarmEvent
	m5 := kafka.NewConsumer(brokers, notify.DefaultTopic, uniqueID("it-m5"))
	defer func() { _ = m5.Close() }()
	go func() {
		// 错误只记录不中断: 消费循环随 ctx 结束而退出, 用例成败由断言决定。
		_ = m5.Consume(ctx, func(_ context.Context, msg kafkago.Message) error {
			var ev notify.AlarmEvent
			if err := json.Unmarshal(msg.Value, &ev); err != nil {
				return nil // 非本用例的消息: 提交位移跳过, 不卡分区
			}
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()
			return nil
		})
	}()

	// ---- 上游: 生产一条真实设备遥测(M1 侧行为) ----
	deviceID := uniqueID("door")
	rid := uniqueID("e2e-kafka")
	body, err := json.Marshal(kafka.DeviceTelemetry{
		RequestID:  rid,
		TenantID:   1,
		DeviceID:   deviceID,
		DeviceType: "access_control",
		EventType:  kafka.AlarmIntrusion,
		ZoneID:     "zone-a",
		OccurredAt: time.Now().Unix(),
		Payload:    []byte(`{"door_status":"open"}`),
		Source:     "mqtt",
	})
	if err != nil {
		t.Fatalf("构造遥测消息失败: %v", err)
	}
	// 分区键用 device_id: 与生产端一致(同一设备事件保序)。
	if err := producer.Publish(ctx, kafka.TopicDeviceTelemetry, []byte(deviceID), body); err != nil {
		t.Fatalf("投递遥测消息失败(检查 broker 与 topic 是否已建): %v", err)
	}

	// ---- M3 侧: 独立消费组消费遥测, 走 HandleWithDeadLetter(与线上消费入口一致) ----
	var once sync.Once
	handled := make(chan error, 1)
	tele := kafka.NewConsumer(brokers, kafka.TopicDeviceTelemetry, uniqueID("it-alarm"))
	defer func() { _ = tele.Close() }()
	go func() {
		_ = tele.Consume(ctx, func(c context.Context, msg kafkago.Message) error {
			var probe struct {
				RequestID string `json:"request_id"`
			}
			// 历史消息(往次运行残留)不是本用例的: 跳过但照常提交位移, 避免卡分区。
			if err := json.Unmarshal(msg.Value, &probe); err != nil || probe.RequestID != rid {
				return nil
			}
			err := svcCtx.HandleWithDeadLetter(c, msg)
			once.Do(func() { handled <- err })
			return err
		})
	}()

	select {
	case err := <-handled:
		if err != nil {
			t.Fatalf("消费遥测消息失败: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("超时: 未从 broker 消费到本用例的遥测消息(检查 broker 地址与 advertised.listeners)")
	}

	// ---- 取证 1: 经 broker 传输后告警仍正确落库 ----
	var row model.Alarm
	if err := db.WithContext(ctx).Where("request_id = ?", rid).First(&row).Error; err != nil {
		t.Fatalf("告警未落库: %v", err)
	}
	t.Logf("取证-落库: alarm_no=%s rule_id=%d level=%d status=%d device_id=%s",
		row.AlarmNo, row.RuleID, row.Level, row.Status, row.DeviceID)
	if row.RuleID != 1 {
		t.Errorf("应命中规则表内的门禁闯入规则(rule_id=1), 实际 %d —— 命中 0 说明走了硬编码回退", row.RuleID)
	}
	if row.Level != model.AlarmLevelMajor {
		t.Errorf("门禁闯入等级应为 3(严重), 实际 %d", row.Level)
	}

	// ---- 取证 2: M5 侧收到事件, 且标识/等级/优先级口径正确 ----
	deadline := time.Now().Add(60 * time.Second)
	var got *notify.AlarmEvent
	for time.Now().Before(deadline) {
		mu.Lock()
		for i := range received {
			if received[i].AlarmID == row.AlarmNo {
				ev := received[i]
				got = &ev
			}
		}
		mu.Unlock()
		if got != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("M5 侧未收到告警事件(期望 alarm_id=%s), 已收 %d 条", row.AlarmNo, len(received))
	}
	t.Logf("取证-M5事件: alarm_id=%s action=%s severity=%d priority=%d status=%d device_id=%s",
		got.AlarmID, got.Action, got.Severity, got.Priority, got.Status, got.DeviceID)
	if got.Action != notify.ActionCreated {
		t.Errorf("动作应为 create, 实际 %q", got.Action)
	}
	if got.Severity != row.Level {
		t.Errorf("severity 应与告警等级一致(M3 语义, 不翻转): event=%d alarm=%d", got.Severity, row.Level)
	}
	// priority 由 dispatch 换算为 M5 语义: 3(严重) → 2(高)。
	if got.Priority != 2 {
		t.Errorf("priority 应为 2(高, M5 语义), 实际 %d", got.Priority)
	}
	if got.Status != model.AlarmStatusPending {
		t.Errorf("产生事件状态应为未处理(0), 实际 %d", got.Status)
	}
}
