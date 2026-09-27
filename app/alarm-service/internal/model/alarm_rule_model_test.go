package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// realAlarmDB 连接真实 MySQL 并应用告警建表脚本(含中文注释, 需 SET NAMES 同批执行).
func realAlarmDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("ALARM_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 ALARM_TEST_DSN, 跳过 MySQL 集成用例")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	abs, err := filepath.Abs("../../../../deploy/sql/m3_mysql_tables.sql")
	if err != nil {
		t.Fatalf("resolve ddl: %v", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read ddl: %v", err)
	}
	// SET NAMES 必须与建表同批执行(连接池换连接会失效), 否则中文注释会被误解析.
	if err := db.Exec("SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;\n" + string(raw)).Error; err != nil {
		t.Fatalf("apply ddl: %v", err)
	}
	return db
}

func newRule(tenantID int64, name, eventType string, status int8) *AlarmRule {
	now := time.Now()
	r := &AlarmRule{
		Name:       name,
		DeviceType: "access_control",
		EventType:  eventType,
		RuleType:   "threshold",
		Conditions: `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gte","value":80}]}`,
		Level:      AlarmLevelMajor,
		Status:     status,
	}
	r.TenantID = tenantID
	r.CreatedAt = now
	r.UpdatedAt = now
	return r
}

// TestIntegration_RuleCRUDAndListEnabled 验证规则写入/更新/禁用/列表与引擎取规则在真实库上的行为.
func TestIntegration_RuleCRUDAndListEnabled(t *testing.T) {
	db := realAlarmDB(t)
	rules := NewAlarmRuleModel(db)
	ctx := context.Background()
	const tenantID = 55
	// 本用例独占该租户: 先清理历史数据, 保证断言不受上一轮残留影响.
	if err := db.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&AlarmRule{}).Error; err != nil {
		t.Fatalf("clean rules: %v", err)
	}

	created := newRule(tenantID, "温度越限", "temperature", RuleStatusEnabled)
	if err := rules.Create(ctx, created); err != nil {
		t.Fatalf("创建规则失败: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("创建后应回填自增ID")
	}

	got, err := rules.FindByID(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("查询规则失败: %v", err)
	}
	if got.Name != "温度越限" || got.EventType != "temperature" {
		t.Errorf("详情与写入不一致: %+v", got)
	}
	// device_type 必须落库并回读出来: 它是「按设备类型 + 事件类型映射等级」的配置维度,
	// 写丢的现象不是报错, 而是规则被静默放宽到全部设备类型。
	if got.DeviceType != "access_control" {
		t.Errorf("device_type 未落库: %q", got.DeviceType)
	}

	// 禁用: status=0 必须能写入(零值更新).
	if err := rules.Update(ctx, tenantID, created.ID, map[string]interface{}{"status": RuleStatusDisabled}); err != nil {
		t.Fatalf("禁用规则失败: %v", err)
	}
	got, _ = rules.FindByID(ctx, tenantID, created.ID)
	if got.Status != RuleStatusDisabled {
		t.Errorf("status=0 未落库(零值被吞): status=%d", got.Status)
	}

	// 启用后再禁用一次, 确认反复切换可用.
	if err := rules.Update(ctx, tenantID, created.ID, map[string]interface{}{"status": RuleStatusEnabled}); err != nil {
		t.Fatalf("启用规则失败: %v", err)
	}

	// 更新不存在的规则应返回 ErrRuleNotFound, 而不是静默成功.
	if err := rules.Update(ctx, tenantID, 999999, map[string]interface{}{"status": 1}); err != ErrRuleNotFound {
		t.Errorf("更新不存在的规则应返回 ErrRuleNotFound, 实际 %v", err)
	}
	if _, err := rules.FindByID(ctx, tenantID, 999999); err != ErrRuleNotFound {
		t.Errorf("查询不存在的规则应返回 ErrRuleNotFound, 实际 %v", err)
	}

	// 跨园区不可见.
	if _, err := rules.FindByID(ctx, tenantID+1, created.ID); err != ErrRuleNotFound {
		t.Errorf("跨园区查询应不可见, 实际 %v", err)
	}

	// 再建一条禁用规则, 验证列表筛选与引擎只取启用规则.
	disabled := newRule(tenantID, "未启用规则", "temperature", RuleStatusDisabled)
	if err := rules.Create(ctx, disabled); err != nil {
		t.Fatalf("创建禁用规则失败: %v", err)
	}

	enabled := int8(1)
	list, total, err := rules.List(ctx, AlarmRuleListFilter{TenantID: tenantID, EventType: "temperature", Status: &enabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("按 status=1 筛选应只返回启用规则: total=%d list=%+v", total, list)
	}

	// 按 device_type 筛选: 验证新维度真的下推到了 SQL, 而不是只加了个字段.
	byType, total, err := rules.List(ctx, AlarmRuleListFilter{
		TenantID: tenantID, DeviceType: "access_control", Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("按设备类型查询失败: %v", err)
	}
	if int(total) != len(byType) || total == 0 {
		t.Errorf("按 device_type 筛选结果异常: total=%d len=%d", total, len(byType))
	}
	// 换一个设备类型应筛不到(否则说明筛选条件没生效, 只是返回了全部).
	other, _, err := rules.List(ctx, AlarmRuleListFilter{
		TenantID: tenantID, DeviceType: "camera", Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("按设备类型查询失败: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("device_type=camera 不应命中 access_control 规则: %+v", other)
	}

	// 引擎取规则: 只返回启用且条件可解析的规则.
	enabledRules, err := rules.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled 失败: %v", err)
	}
	var found bool
	for _, r := range enabledRules {
		if r.ID == created.ID {
			found = true
			// 设备类型必须随规则一起交给引擎, 否则引擎无法按设备类型过滤。
			if r.DeviceType != "access_control" {
				t.Errorf("ListEnabled 未带出 device_type: %q", r.DeviceType)
			}
		}
		if r.ID == disabled.ID {
			t.Error("禁用的规则不应被引擎加载")
		}
	}
	if !found {
		t.Error("启用规则未被引擎加载")
	}
}
