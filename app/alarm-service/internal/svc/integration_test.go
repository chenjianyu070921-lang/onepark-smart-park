package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 集成用例: 依赖真实 MySQL, 未设置 ALARM_TEST_DSN 时全部 Skip, 不影响常规 go test.
//
//	ALARM_TEST_DSN='root:pwd@tcp(127.0.0.1:3306)/alarm_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//
// 注意: 初始化会执行 deploy/sql/m3_mysql_tables.sql, 其中含 DROP TABLE, 请指向专用的本地测试库.
// ddlPath 相对本文件所在目录(internal/svc), 需要上溯四级才到仓库根.
const ddlPath = "../../../../deploy/sql/m3_mysql_tables.sql"

func realDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("ALARM_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 ALARM_TEST_DSN, 跳过 MySQL 集成用例")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	applyDDL(t, db)
	return db
}

// applyDDL 执行建表脚本, 同时验证 deploy/sql/m3_mysql_tables.sql 在真实 MySQL 上可跑通.
func applyDDL(t *testing.T, db *gorm.DB) {
	t.Helper()
	abs, err := filepath.Abs(ddlPath)
	if err != nil {
		t.Fatalf("resolve ddl path: %v", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read ddl %s: %v", abs, err)
	}
	// 建表脚本含中文注释, 连接字符集必须为 utf8mb4, 否则中文里出现的 0x5C 会被当成转义符导致 1064.
	// SET NAMES 必须与建表语句在同一批执行, 否则连接池换连接会失效.
	script := "SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;\n" + string(raw)
	if err := db.Exec(script).Error; err != nil {
		t.Fatalf("apply ddl %s: %v", abs, err)
	}
}

func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func sampleAlarm(requestID string, level int8) *model.Alarm {
	now := time.Now()
	a := &model.Alarm{
		AlarmNo:   NewAlarmNo(requestID, now),
		DeviceID:  "door-01",
		AreaID:    12,
		EventType: EventTypeIntrusion,
		Level:     level,
		Status:    model.AlarmStatusPending,
		Content:   "集成测试告警",
		RequestID: requestID,
	}
	a.TenantID = 1
	a.CreatedAt = now
	a.UpdatedAt = now
	return a
}

// TestIntegration_CreateDuplicateByRequestID 验收 P0-5(L3): MySQL uk_request_id 真实拦截重复写入.
func TestIntegration_CreateDuplicateByRequestID(t *testing.T) {
	db := realDB(t)
	alarms := model.NewAlarmModel(db)
	ctx := context.Background()

	rid := uniqueID("p05")

	first := sampleAlarm(rid, model.AlarmLevelMinor)
	if err := alarms.Create(ctx, first); err != nil {
		t.Fatalf("首次写入应成功: %v", err)
	}
	err := alarms.Create(ctx, sampleAlarm(rid, model.AlarmLevelMinor))
	if !errors.Is(err, model.ErrDuplicateRequest) {
		t.Fatalf("重复 request_id 应返回 ErrDuplicateRequest, 实际 %v", err)
	}

	var cnt int64
	if e := db.WithContext(ctx).Model(&model.Alarm{}).Where("request_id = ?", rid).Count(&cnt).Error; e != nil {
		t.Fatalf("count: %v", e)
	}
	if cnt != 1 {
		t.Errorf("唯一索引未生效: 期望 1 行, 实际 %d 行", cnt)
	}

	// 生成动作必须留下 action=create 的审计流水(与 DDL 注释及 AlarmActionCreate 语义一致).
	var logCnt int64
	if e := db.WithContext(ctx).Model(&model.AlarmOperateLog{}).
		Where("alarm_id = ? AND action = ?", first.ID, model.AlarmActionCreate).Count(&logCnt).Error; e != nil {
		t.Fatalf("count create log: %v", e)
	}
	if logCnt != 1 {
		t.Errorf("期望 1 条 create 审计流水, 实际 %d 条", logCnt)
	}
}

// TestIntegration_ListDetailAndTransition 验收 P0-3: 列表筛选 / 详情 / 确认→解决 在真实库上的行为.
func TestIntegration_ListDetailAndTransition(t *testing.T) {
	db := realDB(t)
	alarms := model.NewAlarmModel(db)
	ctx := context.Background()

	idPrefix := uniqueID("p03")
	p2 := idPrefix + "-p2"
	p4 := idPrefix + "-p4"
	if err := alarms.Create(ctx, sampleAlarm(p2, model.AlarmLevelMinor)); err != nil {
		t.Fatalf("seed p2: %v", err)
	}
	if err := alarms.Create(ctx, sampleAlarm(p4, model.AlarmLevelCritical)); err != nil {
		t.Fatalf("seed p4: %v", err)
	}

	// 按等级筛选(level=4)应只命中 1 条, 且总数口径正确.
	lv := model.AlarmLevelCritical
	st := model.AlarmStatusPending
	list, total, err := alarms.List(ctx, model.AlarmListFilter{
		TenantID: 1, Status: &st, Level: &lv, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("等级筛选异常: total=%d len=%d", total, len(list))
	}
	if list[0].Level != model.AlarmLevelCritical {
		t.Errorf("期望仅返回 level=4, 实际 %d", list[0].Level)
	}

	// 详情 + 状态流转: 未处理 → 已确认 → 已解决.
	got, err := alarms.FindByID(ctx, 1, list[0].ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.RequestID != p4 {
		t.Errorf("详情 request_id 不匹配: %s", got.RequestID)
	}

	now := time.Now()
	if err := alarms.Ack(ctx, 1, got.ID, 8, "集成: 已派安保", now); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := alarms.Ack(ctx, 1, got.ID, 8, "重复确认", now); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("重复 ack 应被前置状态拦截, 实际 %v", err)
	}
	if err := alarms.Resolve(ctx, 1, got.ID, 8, "集成: 已处理", now); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	after, err := alarms.FindByID(ctx, 1, got.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status != model.AlarmStatusResolved || after.AckBy != 8 || after.ResolveBy != 8 || after.AckAt == nil || after.ResolveAt == nil {
		t.Errorf("状态流转结果异常: %+v", after)
	}

	// 审计流水应记录 ack 与 resolve 两条.
	var logCnt int64
	if e := db.WithContext(ctx).Model(&model.AlarmOperateLog{}).
		Where("alarm_id = ? AND action IN ?", got.ID, []string{model.AlarmActionAck, model.AlarmActionResolve}).
		Count(&logCnt).Error; e != nil {
		t.Fatalf("count log: %v", e)
	}
	if logCnt != 2 {
		t.Errorf("期望 2 条审计流水, 实际 %d 条", logCnt)
	}
}

// TestIntegration_CountActive 验收 P0-4: GetActiveAlarms 数据源 CountActive 在真实库聚合正确.
func TestIntegration_CountActive(t *testing.T) {
	db := realDB(t)
	alarms := model.NewAlarmModel(db)
	ctx := context.Background()

	idPrefix := uniqueID("p04")
	// 3 条未处理(其中 level3 一条、level4 一条、level2 一条) + 1 条已解决(不计入活跃).
	for _, lv := range []int8{model.AlarmLevelMinor, model.AlarmLevelMajor, model.AlarmLevelCritical} {
		if err := alarms.Create(ctx, sampleAlarm(idPrefix+fmt.Sprintf("-%d", lv), lv)); err != nil {
			t.Fatalf("seed level %d: %v", lv, err)
		}
	}
	resolved := sampleAlarm(idPrefix+"-done", model.AlarmLevelMinor)
	resolved.Status = model.AlarmStatusResolved
	if err := alarms.Create(ctx, resolved); err != nil {
		t.Fatalf("seed resolved: %v", err)
	}

	total, counts, err := alarms.CountActive(ctx, 1, 0, nil)
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if total < 3 {
		t.Errorf("活跃告警总数应 >=3(至少本次播种的 3 条), 实际 %d", total)
	}

	byLevel := make(map[int8]int64, len(counts))
	for _, c := range counts {
		byLevel[c.Level] = c.Total
	}
	for _, lv := range []int8{model.AlarmLevelMinor, model.AlarmLevelMajor, model.AlarmLevelCritical} {
		if byLevel[lv] < 1 {
			t.Errorf("等级 %d 应有至少 1 条活跃告警, 分布=%v", lv, byLevel)
		}
	}
	// 聚合总数必须与「直接数 status=0 的行数」一致, 即已解决告警不计入活跃.
	var expect int64
	if err := db.WithContext(ctx).Model(&model.Alarm{}).
		Where("tenant_id = ? AND status = ?", 1, model.AlarmStatusPending).Count(&expect).Error; err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if total != expect {
		t.Errorf("活跃总数与 status=0 行数不一致: total=%d expect=%d", total, expect)
	}
}

// TestIntegration_CountActiveFilters 验收 #43 的过滤语义:
// tenant_id/area_id 为 0 表示不参与过滤, levels 为空表示全部等级(契约出处: proto/alarm/alarm.proto).
// 其中 tenant_id=0 = 跨园区聚合是 M5 大屏的必经路径 —— 其 AlarmProvider.Stat(ctx) 无租户入参, 只能传 0.
func TestIntegration_CountActiveFilters(t *testing.T) {
	db := realDB(t)
	alarms := model.NewAlarmModel(db)
	ctx := context.Background()

	const tenantA, tenantB = int64(71), int64(72)
	prefix := uniqueID("p43")
	seed := func(tenantID, areaID int64, level int8, suffix string) {
		a := sampleAlarm(prefix+suffix, level)
		a.TenantID = tenantID
		a.AreaID = areaID
		if err := alarms.Create(ctx, a); err != nil {
			t.Fatalf("seed %s: %v", suffix, err)
		}
	}
	// A 园区两个区域各一条(等级 4/3), B 园区一条(等级 2); 用于验证租户、区域、等级三种过滤互不串味.
	seed(tenantA, 100, model.AlarmLevelCritical, "-a1")
	seed(tenantA, 101, model.AlarmLevelMajor, "-a2")
	seed(tenantB, 200, model.AlarmLevelMinor, "-b1")

	// 期望值一律以同条件的直查为准, 使用例对库内历史残留不敏感(可重复执行).
	directCount := func(where string, args ...interface{}) int64 {
		var n int64
		if err := db.WithContext(ctx).Model(&model.Alarm{}).
			Where("status = ?", model.AlarmStatusPending).Where(where, args...).Count(&n).Error; err != nil {
			t.Fatalf("direct count (%s): %v", where, err)
		}
		return n
	}

	// 1) tenant_id=0 → 不过滤租户, 等于全库活跃告警数(跨园区).
	all, _, err := alarms.CountActive(ctx, 0, 0, nil)
	if err != nil {
		t.Fatalf("count all: %v", err)
	}
	if expect := directCount("1 = 1"); all != expect {
		t.Errorf("tenant_id=0 应聚合全部园区: total=%d 期望 %d", all, expect)
	}
	if all <= 0 {
		t.Fatal("tenant_id=0 不应返回空聚合(M5 大屏依赖该语义)")
	}

	// 2) 指定租户 → 只统计该园区, 且必须小于跨园区总数(证明过滤真的下推了).
	scoped, _, err := alarms.CountActive(ctx, tenantA, 0, nil)
	if err != nil {
		t.Fatalf("count tenant: %v", err)
	}
	if expect := directCount("tenant_id = ?", tenantA); scoped != expect {
		t.Errorf("单园区聚合口径异常: total=%d 期望 %d", scoped, expect)
	}
	if scoped >= all {
		t.Errorf("单园区聚合应小于跨园区聚合: scoped=%d all=%d", scoped, all)
	}

	// 3) area_id≠0 → 按区域收敛, 必须小于本园区总数(园区内确有其他区域的告警).
	byArea, _, err := alarms.CountActive(ctx, tenantA, 100, nil)
	if err != nil {
		t.Fatalf("count area: %v", err)
	}
	if expect := directCount("tenant_id = ? AND area_id = ?", tenantA, 100); byArea != expect {
		t.Errorf("区域过滤口径异常: total=%d 期望 %d", byArea, expect)
	}
	if byArea >= scoped {
		t.Errorf("区域过滤应小于园区总数: byArea=%d scoped=%d", byArea, scoped)
	}

	// 4) levels 非空 → 只保留命中等级, 且分布只含该等级.
	minor := []int32{int32(model.AlarmLevelMinor)}
	byLevel, counts, err := alarms.CountActive(ctx, tenantA, 0, minor)
	if err != nil {
		t.Fatalf("count levels: %v", err)
	}
	if expect := directCount("tenant_id = ? AND level IN ?", tenantA, []int8{model.AlarmLevelMinor}); byLevel != expect {
		t.Errorf("等级过滤口径异常: total=%d 期望 %d", byLevel, expect)
	}
	for _, c := range counts {
		if c.Level != model.AlarmLevelMinor {
			t.Errorf("等级过滤后分布不应含 level=%d: %+v", c.Level, counts)
		}
	}
	// A 园区播种的是 4/3 级, 按 2 级筛选应被完全过滤掉; 若历史残留命中, 至少必须小于园区总数.
	if byLevel >= scoped {
		t.Errorf("等级过滤未生效: byLevel=%d scoped=%d", byLevel, scoped)
	}

	// 5) 总数与按等级分布必须自洽: 分布求和恒等于总数.
	_, counts, err = alarms.CountActive(ctx, tenantA, 0, nil)
	if err != nil {
		t.Fatalf("count distribution: %v", err)
	}
	var sum int64
	for _, c := range counts {
		sum += c.Total
	}
	if sum != scoped {
		t.Errorf("等级分布求和与总数不一致: sum=%d total=%d", sum, scoped)
	}
}

// TestIntegration_ConsumerCreatesAlarmInRealDB 验收 P0-2 + P0-5: 消费链路在真实库端点对端跑一次.
func TestIntegration_ConsumerCreatesAlarmInRealDB(t *testing.T) {
	db := realDB(t)
	ctx := context.Background()

	svcCtx := &ServiceContext{
		Alarms: model.NewAlarmModel(db),
		Dedup:  &fakeDeduper{seen: map[string]bool{}},
	}

	rid := uniqueID("e2e")
	msg := intrusionEvent(t, rid)
	for i := 0; i < 3; i++ {
		if err := svcCtx.HandleDeviceEvent(ctx, msg); err != nil {
			t.Fatalf("第 %d 次消费出错: %v", i+1, err)
		}
	}

	var row model.Alarm
	if err := db.WithContext(ctx).Where("request_id = ?", rid).First(&row).Error; err != nil {
		t.Fatalf("告警未落库: %v", err)
	}
	// 等级取 AlarmLevelMajor(3 "严重"): 门禁闯入的等级口径为"高",
	// 规则表内的种子规则①与硬编码回退分支必须同档(2026-09-22 对齐)。
	if row.Level != model.AlarmLevelMajor || row.Status != model.AlarmStatusPending {
		t.Errorf("落库内容异常: level=%d status=%d", row.Level, row.Status)
	}
	if !strings.HasPrefix(row.AlarmNo, "AL") {
		t.Errorf("alarm_no 格式异常: %s", row.AlarmNo)
	}

	var cnt int64
	if err := db.WithContext(ctx).Model(&model.Alarm{}).Where("request_id = ?", rid).Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Errorf("同 request_id 消费 3 次应只有 1 条告警, 实际 %d 条", cnt)
	}
}
