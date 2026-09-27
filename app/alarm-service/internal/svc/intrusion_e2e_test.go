package svc

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/ws"
	"onepark/common/redisx"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// 门禁闯入告警主链路端到端验收(真实 MySQL + 真实 Redis + 真实 WebSocket).
//
// 覆盖: M1 设备事件 → 规则引擎匹配"门禁闯入"规则(等级 3) → Redis 三层去重 → 落库
//      → WebSocket 广播到大屏。
//
// 为什么不用 Kafka 起真 broker: 本机无 JVM 且 Docker daemon 不可用, Kafka 传输层无法启动。
// 这里直接驱动消费入口 HandleDeviceEvent —— 与 kafka.Consumer 交给 HandleWithDeadLetter
// 的调用是同一入口同一入参(Kafka 只负责搬运 Message, 不参与任何判定), 判定/去重/落库/推送
// 全链路均为真实组件, 唯一未覆盖的是 Kafka 的传输与位移提交(见交付说明的已知限制)。
//
// 运行:
//	ALARM_TEST_DSN='root:@tcp(127.0.0.1:3399)/alarm_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//	ALARM_TEST_REDIS_ADDR='127.0.0.1:6379'
//	go test ./internal/svc/ -run TestE2E_IntrusionAlarmChain -v
func TestE2E_IntrusionAlarmChain(t *testing.T) {
	db := realDB(t) // 未设置 ALARM_TEST_DSN 时自动 Skip
	redisAddr := os.Getenv("ALARM_TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("未设置 ALARM_TEST_REDIS_ADDR, 跳过端到端验收(需要真实 Redis)")
	}
	ctx := context.Background()

	// ---- 装配: 与 NewServiceContext 完全一致, 全部真实组件 ----
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis 不可用: %v", err)
	}

	rules := model.NewAlarmRuleModel(db)
	hub := ws.NewHub()
	svcCtx := &ServiceContext{
		Alarms:   model.NewAlarmModel(db),
		Rules:    rules,
		Engine:   rule.NewEngine(rules, rule.NewRedisWindow(rdb), 30*time.Second),
		Dedup:    dedup.NewRedisDeduper(rdb, dedupTTL),
		Cooldown: dedup.NewRedisCooldown(rdb),
		Hub:      hub,
		Redis:    rdb,
	}

	// ---- 大屏接入: 真实 HTTP Upgrade + 真实 WebSocket 客户端 ----
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/alarm?tenant_id=1", nil)
	if err != nil {
		t.Fatalf("大屏连接 /ws/alarm 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// ---- 事件: 门禁设备上报 intrusion ----
	// 设备号取唯一值: 库内其它集成用例也用 door-01 播种, 用固定设备号会让"仅本用例产生 1 条"
	// 的断言随包内执行顺序漂移(单独跑 PASS、整包跑 FAIL)。
	deviceID := uniqueID("door")
	rid := uniqueID("e2e-intrusion")
	msg := intrusionEventFor(t, rid, deviceID)
	if err := svcCtx.HandleDeviceEvent(ctx, msg); err != nil {
		t.Fatalf("消费门禁闯入事件失败: %v", err)
	}

	// ---- 取证 1: 告警已落库, 且命中规则①(而非 rule_id=0 的硬编码回退) ----
	var row model.Alarm
	if err := db.WithContext(ctx).Where("request_id = ?", rid).First(&row).Error; err != nil {
		t.Fatalf("告警未落库: %v", err)
	}
	t.Logf("取证-落库: alarm_no=%s rule_id=%d level=%d status=%d device_id=%s tenant_id=%d content=%s",
		row.AlarmNo, row.RuleID, row.Level, row.Status, row.DeviceID, row.TenantID, row.Content)
	if row.RuleID != 1 {
		t.Errorf("应命中规则表内的门禁闯入规则(rule_id=1), 实际 rule_id=%d —— 命中 %d 说明走了硬编码回退", row.RuleID, row.RuleID)
	}
	if row.Level != model.AlarmLevelMajor {
		t.Errorf("门禁闯入等级应为 3(严重), 实际 %d", row.Level)
	}
	if row.Status != model.AlarmStatusPending || row.DeviceID != deviceID || row.TenantID != 1 {
		t.Errorf("落库内容与事件不一致: %+v", row)
	}

	// ---- 取证 2: 大屏通过 WebSocket 收到 alarm.created, 且带等级 ----
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("大屏未收到 WebSocket 推送: %v", err)
	}
	var env struct {
		Type      string          `json:"type"`
		RequestId string          `json:"request_id"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("推送报文解析失败: %v", err)
	}
	if env.Type != ws.TypeAlarmCreated {
		t.Errorf("推送类型应为 %s, 实际 %s", ws.TypeAlarmCreated, env.Type)
	}
	var data ws.AlarmEvent
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("推送载荷解析失败: %v", err)
	}
	t.Logf("取证-WS推送: type=%s request_id=%s alarm_id=%s level=%d event_type=%s content=%s",
		env.Type, env.RequestId, data.AlarmID, data.Level, data.EventType, data.Content)
	if data.Level != model.AlarmLevelMajor {
		t.Errorf("推送等级应与告警一致(3), 实际 %d —— 大屏据此触发声音/闪烁", data.Level)
	}
	// WS 推送与 Kafka 通知 M5 必须用同一个告警标识: 业务编号 alarm_no(string), 而非物理主键 id。
	// 用 id 时前端与 M5 拿到的值与告警列表/详情接口的 alarm_no 对不上, 跨模块无法串联。
	if data.AlarmID != row.AlarmNo || data.DeviceID != deviceID {
		t.Errorf("推送载荷与落库告警不一致: push=%+v db=%+v", data, row)
	}

	// ---- 取证 3: L1 消息级去重 —— 同一 request_id 重投不产生第二条告警 ----
	if err := svcCtx.HandleDeviceEvent(ctx, msg); err != nil {
		t.Fatalf("重投同一消息不应报错: %v", err)
	}
	if n := countByRequestID(t, db, rid); n != 1 {
		t.Errorf("L1 去重失效: request_id=%s 应有 1 条, 实际 %d 条", rid, n)
	}

	// ---- 取证 4: L2 业务冷却 —— 同设备同事件 60s 内不重复告警 ----
	rid2 := uniqueID("e2e-intrusion")
	if err := svcCtx.HandleDeviceEvent(ctx, intrusionEventFor(t, rid2, deviceID)); err != nil {
		t.Fatalf("冷却窗口内的第二次上报不应报错: %v", err)
	}
	if n := countByDevice(t, db, deviceID); n != 1 {
		t.Errorf("L2 冷却失效: 设备 %s 应有 1 条告警, 实际 %d 条", deviceID, n)
	}
	t.Logf("取证-去重: L1 重投后 %d 条(同 request_id), L2 冷却后 %d 条(同设备)",
		countByRequestID(t, db, rid), countByDevice(t, db, deviceID))
}

// intrusionEventFor 构造指定设备号的门禁闯入事件; 与 intrusionEvent 同源, 仅设备号可注入,
// 便于端到端用例与其它用例在真实库里互不干扰.
func intrusionEventFor(t *testing.T, requestID, deviceID string) kafkago.Message {
	t.Helper()
	body, err := json.Marshal(DeviceEvent{
		RequestID:  requestID,
		TenantID:   1,
		DeviceID:   deviceID,
		DeviceType: DeviceTypeAccessControl,
		EventType:  EventTypeIntrusion,
		AreaID:     12,
		OccurredAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return kafkago.Message{Topic: "device-telemetry", Partition: 0, Offset: 1, Value: body}
}

func countByRequestID(t *testing.T, db *gorm.DB, requestID string) int64 {
	t.Helper()
	var n int64
	if err := db.WithContext(context.Background()).Model(&model.Alarm{}).
		Where("request_id = ?", requestID).Count(&n).Error; err != nil {
		t.Fatalf("count by request_id: %v", err)
	}
	return n
}

func countByDevice(t *testing.T, db *gorm.DB, deviceID string) int64 {
	t.Helper()
	var n int64
	if err := db.WithContext(context.Background()).Model(&model.Alarm{}).
		Where("device_id = ? AND event_type = ?", deviceID, EventTypeIntrusion).
		Where("created_at >= ?", time.Now().Add(-time.Hour)).Count(&n).Error; err != nil {
		t.Fatalf("count by device: %v", err)
	}
	return n
}
