package consumer

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 用内存 SQLite 建出 work_order / work_order_flow 两张表, 用于验证"真实落库".
// ⚠️ 仅测试使用, 不影响生产库; 生产由 deploy/sql/m2_mysql_tables.sql 建表.
// 内存库必须限制单连接, 否则连接池每个连接是独立的空库, 表不可见.
// 这里用原始 DDL 而非 GORM AutoMigrate: 模型在多个表上共用同名索引 idx_tenant,
// 在 MySQL 合法, 但 SQLite 要求库内索引名唯一, 故测试 DDL 只保留落库/幂等必需的
// 列与 UNIQUE 约束(order_no / alarm_id), 不创建同名索引, 以规避该差异.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if sqlDB, err := db.DB(); err != nil {
		t.Fatalf("获取底层 sql.DB 失败: %v", err)
	} else {
		sqlDB.SetMaxOpenConns(1)
	}
	ddl := []string{
		`CREATE TABLE work_order (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			order_no VARCHAR(32) NOT NULL,
			type TINYINT NOT NULL,
			title VARCHAR(128) NOT NULL,
			description VARCHAR(1024),
			reporter_id BIGINT NOT NULL,
			assignee_id BIGINT NOT NULL DEFAULT 0,
			department_id BIGINT NOT NULL DEFAULT 0,
			status TINYINT NOT NULL DEFAULT 0,
			priority TINYINT NOT NULL DEFAULT 2,
			location VARCHAR(128),
			attachments VARCHAR(1024),
			version BIGINT NOT NULL DEFAULT 0,
			finished_at DATETIME,
			alarm_id VARCHAR(64),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			CONSTRAINT uk_order_no UNIQUE (order_no),
			CONSTRAINT uk_alarm_id UNIQUE (alarm_id)
		)`,
		`CREATE TABLE work_order_flow (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			work_order_id BIGINT NOT NULL,
			from_status TINYINT,
			to_status TINYINT NOT NULL,
			action VARCHAR(32) NOT NULL,
			operator_id BIGINT NOT NULL,
			remark VARCHAR(512),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	}
	for _, d := range ddl {
		if err := db.Exec(d).Error; err != nil {
			t.Fatalf("建测试表失败: %v", err)
		}
	}
	return db
}

// sampleAlarm 模拟 M1 event-dispatcher 投递到 alarm-event 的真实报文(字段与 dispatch.Message 对齐).
// 注意: 真实生产者把 Message.Payload(json.RawMessage) 整体 marshal, payload 在线上是"对象"而非"字符串";
// 这里用 json.RawMessage 嵌入, 复现线上形态, 否则消费者内部二次反序列化会静默失败.
func sampleAlarm() []byte {
	payloadObj, _ := json.Marshal(map[string]interface{}{
		"tenant_id": 1,
		"zone_code": "A-3",
		"location":  "3号楼",
	})
	msg, _ := json.Marshal(map[string]interface{}{
		"request_id":  "req-abc-123",
		"device_id":   "dev-smoke-01",
		"device_type": "smoke_detector",
		"event_type":  "smoke", // 烟雾告警 → 紧急工单
		"occurred_at": 1758000000,
		"payload":     json.RawMessage(payloadObj),
		"source":      "mqtt",
	})
	return msg
}

// TestAlarmPersist_LandsWorkOrder 验证告警消费能真实落库(主表 + 建单流水同事务).
func TestAlarmPersist_LandsWorkOrder(t *testing.T) {
	db := newTestDB(t)
	h := NewAlarmHandler(db, nil) // producer 传 nil: 不依赖 Kafka 广播

	if err := h.Handle(context.Background(), sampleAlarm(), 1); err != nil {
		t.Fatalf("Handle 返回错误(应成功落库): %v", err)
	}

	var woCount int64
	db.Model(&model.WorkOrder{}).Count(&woCount)
	if woCount != 1 {
		t.Fatalf("期望落库 1 条工单, 实际 %d", woCount)
	}
	var flowCount int64
	db.Model(&model.WorkOrderFlow{}).Count(&flowCount)
	if flowCount != 1 {
		t.Fatalf("期望落库 1 条建单流水, 实际 %d", flowCount)
	}

	// 校验落库的工单字段符合预期(报修/紧急/待派单 + 幂等键 + 租户 + 位置).
	var wo model.WorkOrder
	if err := db.Where("alarm_id = ?", "req-abc-123").First(&wo).Error; err != nil {
		t.Fatalf("查回工单失败: %v", err)
	}
	if !regexp.MustCompile(model.OrderNoPattern).MatchString(wo.OrderNo) {
		t.Errorf("工单号格式不符: %s", wo.OrderNo)
	}
	if wo.Type != model.WorkOrderTypeRepair {
		t.Errorf("工单类型应为报修(1), 实际 %d", wo.Type)
	}
	if wo.Status != state.StatusPendingDispatch {
		t.Errorf("工单状态应为待派单(0), 实际 %d", wo.Status)
	}
	if wo.Priority != model.PriorityUrgent {
		t.Errorf("烟雾告警应为紧急(1), 实际 %d", wo.Priority)
	}
	if wo.TenantID != 1 {
		t.Errorf("租户应取 payload.tenant_id=1, 实际 %d", wo.TenantID)
	}
	if wo.Location != "A-3" {
		t.Errorf("位置应取 zone_code=A-3, 实际 %q", wo.Location)
	}

	// 校验建单流水: action=create, 前态 -1, 后态 待派单, 且关联到本工单.
	var flow model.WorkOrderFlow
	if err := db.Where("work_order_id = ?", wo.ID).First(&flow).Error; err != nil {
		t.Fatalf("查回流水失败: %v", err)
	}
	if flow.Action != state.ActionCreate || flow.FromStatus != -1 || flow.ToStatus != wo.Status {
		t.Errorf("建单流水内容异常: %+v", flow)
	}
}

// TestAlarmPersist_Idempotent 验证同一告警重复投递不会落重单(uk_alarm_id 唯一键幂等).
func TestAlarmPersist_Idempotent(t *testing.T) {
	db := newTestDB(t)
	h := NewAlarmHandler(db, nil)

	if err := h.Handle(context.Background(), sampleAlarm(), 1); err != nil {
		t.Fatalf("首次 Handle 失败: %v", err)
	}
	// 第二次用同一 request_id 投递(模拟重投/多实例并发消费).
	// ⚠️ 注意: MySQL 下 uk_alarm_id 冲突会被识别为幂等并跳过(返回 nil);
	// SQLite 下唯一键冲突文本不含索引名, 会走"建单失败"分支返回 error,
	// 但事务已回滚, 不会落重单 —— 数据幂等由唯一约束保证, 故此处只断言行数.
	if err := h.Handle(context.Background(), sampleAlarm(), 1); err != nil {
		t.Logf("二次投递返回(取决于 DB 错误文本, 属预期内): %v", err)
	}

	var woCount int64
	db.Model(&model.WorkOrder{}).Count(&woCount)
	if woCount != 1 {
		t.Fatalf("重复告警应只落 1 条工单, 实际 %d", woCount)
	}
}

// TestAlarmPersist_DifferentAlarmSeparate 验证不同告警各自落单, 互不干扰.
func TestAlarmPersist_DifferentAlarmSeparate(t *testing.T) {
	db := newTestDB(t)
	h := NewAlarmHandler(db, nil)

	if err := h.Handle(context.Background(), sampleAlarm(), 1); err != nil {
		t.Fatalf("首个告警落库失败: %v", err)
	}
	// 第二个告警: 换 request_id, 换成 fault(普通工单).
	other := sampleAlarm()
	otherMap := map[string]interface{}{}
	_ = json.Unmarshal(other, &otherMap)
	otherMap["request_id"] = "req-def-456"
	otherMap["event_type"] = "fault"
	other, _ = json.Marshal(otherMap)
	if err := h.Handle(context.Background(), other, 1); err != nil {
		t.Fatalf("第二个告警落库失败: %v", err)
	}

	var woCount int64
	db.Model(&model.WorkOrder{}).Count(&woCount)
	if woCount != 2 {
		t.Fatalf("两条不同告警应落 2 单, 实际 %d", woCount)
	}

	var urgent, normal int64
	db.Model(&model.WorkOrder{}).Where("priority = ?", model.PriorityUrgent).Count(&urgent)
	db.Model(&model.WorkOrder{}).Where("priority = ?", model.PriorityNormal).Count(&normal)
	if urgent != 1 || normal != 1 {
		t.Fatalf("优先级分布异常: urgent=%d normal=%d", urgent, normal)
	}
}

// TestAlarmPersist_IgnoreNonRepairAlarm 验证停车类异常车告警(无 request_id)被安全跳过, 不落单.
// 这类告警由 parking-service 投递, 形状与 event-dispatcher 不同, 本消费者不应误建报修单.
func TestAlarmPersist_IgnoreNonRepairAlarm(t *testing.T) {
	db := newTestDB(t)
	h := NewAlarmHandler(db, nil)

	// 模拟 parking-service 的 alarmMsg 形状: 只有 device_id/severity/content/timestamp.
	parkingAlarm, _ := json.Marshal(map[string]interface{}{
		"device_id": "mag-07",
		"severity":  2,
		"content":   "异常车辆 沪A·12345 离场",
		"timestamp": 1758000000,
	})
	// 缺少 request_id → 解码失败 → 记录后跳过(返回 nil, 不落单).
	if err := h.Handle(context.Background(), parkingAlarm, 1); err != nil {
		t.Fatalf("非报修告警应被安全跳过(返回 nil), 实际 err=%v", err)
	}

	var woCount int64
	db.Model(&model.WorkOrder{}).Count(&woCount)
	if woCount != 0 {
		t.Fatalf("非报修告警不应落单, 实际 %d", woCount)
	}
}
