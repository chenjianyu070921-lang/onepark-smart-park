package model

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 集成用例: 依赖真实 MySQL(video_db), 未设置 VIDEO_TEST_DSN 时全部 Skip.
// 与 camera_model_test.go 共用 realVideoDB —— 建表脚本已含 record_plan, 一并执行.
//
// 复用一个高数值的 camera_id 段避免污染既有数据, 用例结束按 tenant_id 清理本轮写入.
const (
	planTestTenant  = int64(909901)
	planTestCameraA = int64(909901)
	planTestCameraB = int64(909902)
)

func cleanupPlans(t *testing.T, m RecordPlanModel) {
	t.Helper()
	ctx := context.Background()
	list, _, err := m.List(ctx, RecordPlanListFilter{TenantID: planTestTenant, Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("清理前查询失败: %v", err)
	}
	for _, p := range list {
		if err := m.Delete(ctx, p.TenantID, p.ID); err != nil {
			t.Fatalf("清理失败 id=%d: %v", p.ID, err)
		}
	}
}

func newPlan(t *testing.T, m RecordPlanModel, cameraID int64, name string) *RecordPlan {
	t.Helper()
	now := time.Now()
	p := &RecordPlan{
		Name:          name,
		CameraID:      cameraID,
		Strategy:      RecordStrategyAlways,
		StartMinute:   0,
		EndMinute:     minutesPerDay,
		RetentionDays: 7,
		Status:        RecordPlanStatusEnabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	p.TenantID = planTestTenant
	if err := m.Create(context.Background(), p); err != nil {
		t.Fatalf("创建测试计划失败: %v", err)
	}
	return p
}

// TestIntegration_RecordPlanCRUD 真实 MySQL 上的录像计划读写路径(建表脚本含本表).
func TestIntegration_RecordPlanCRUD(t *testing.T) {
	db := realVideoDB(t)
	m := NewRecordPlanModel(db)
	ctx := context.Background()
	cleanupPlans(t, m)
	t.Cleanup(func() { cleanupPlans(t, m) })

	p := newPlan(t, m, planTestCameraA, "集成-全天")

	got, err := m.FindByID(ctx, planTestTenant, p.ID)
	if err != nil {
		t.Fatalf("按ID查询失败: %v", err)
	}
	if got.Name != "集成-全天" || got.Status != RecordPlanStatusEnabled || got.RetentionDays != 7 {
		t.Errorf("落库字段与读出不一致: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at 未写入")
	}

	// 同摄像头重名必须转译为业务错误, 而不是暴露 MySQL 1062.
	dup := *got
	dup.ID = 0
	if err := m.Create(ctx, &dup); err == nil {
		t.Error("同摄像头重名应返回错误")
	} else if !errors.Is(err, ErrRecordPlanDuplicate) {
		t.Errorf("期望 ErrRecordPlanDuplicate, 实际 %v", err)
	}

	// 增量更新: 只改保留天数, 名称保持不变.
	days := 30
	if err := m.Update(ctx, planTestTenant, p.ID, RecordPlanPatch{RetentionDays: &days}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	updated, err := m.FindByID(ctx, planTestTenant, p.ID)
	if err != nil {
		t.Fatalf("更新后查询失败: %v", err)
	}
	if updated.RetentionDays != 30 {
		t.Errorf("保留天数应为 30, 实际 %d", updated.RetentionDays)
	}
	if updated.Name != "集成-全天" {
		t.Errorf("未传入的字段不应被改动: %q", updated.Name)
	}
}

// TestIntegration_RecordPlanListAndEnabledScope 列表筛选与"仅启用计划"的隔离:
// 回放推导只认启用计划, 若这里把停用的一并返回, 停用的计划会继续产出回放窗口.
func TestIntegration_RecordPlanListAndEnabledScope(t *testing.T) {
	db := realVideoDB(t)
	m := NewRecordPlanModel(db)
	ctx := context.Background()
	cleanupPlans(t, m)
	t.Cleanup(func() { cleanupPlans(t, m) })

	a := newPlan(t, m, planTestCameraA, "集成-A")
	b := newPlan(t, m, planTestCameraB, "集成-B")
	disabled := int8(RecordPlanStatusDisabled)
	if err := m.Update(ctx, planTestTenant, b.ID, RecordPlanPatch{Status: &disabled}); err != nil {
		t.Fatalf("停用计划失败: %v", err)
	}

	list, total, err := m.List(ctx, RecordPlanListFilter{TenantID: planTestTenant, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("应查到 2 条, 实际 total=%d len=%d", total, len(list))
	}
	if list[0].ID != b.ID {
		t.Errorf("应按 id DESC 排列, 首个为 %d", list[0].ID)
	}

	byCamera, totalByCamera, err := m.List(ctx, RecordPlanListFilter{
		TenantID: planTestTenant, CameraID: planTestCameraA, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("按摄像头筛选失败: %v", err)
	}
	if totalByCamera != 1 || byCamera[0].ID != a.ID {
		t.Errorf("按摄像头筛选异常: total=%d first=%+v", totalByCamera, byCamera)
	}

	status := int8(RecordPlanStatusEnabled)
	enabledOnly, totalEnabled, err := m.List(ctx, RecordPlanListFilter{
		TenantID: planTestTenant, Status: &status, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("按状态筛选失败: %v", err)
	}
	if totalEnabled != 1 || enabledOnly[0].ID != a.ID {
		t.Errorf("status=1 应只剩启用的 A: total=%d first=%+v", totalEnabled, enabledOnly)
	}

	// 回放侧唯一数据源: 只返回启用计划.
	enabled, err := m.ListEnabledByCamera(ctx, planTestTenant, planTestCameraA)
	if err != nil {
		t.Fatalf("查询启用计划失败: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != a.ID {
		t.Errorf("ListEnabledByCamera 应只返回启用的 A: %+v", enabled)
	}
	none, err := m.ListEnabledByCamera(ctx, planTestTenant, planTestCameraB)
	if err != nil {
		t.Fatalf("查询启用计划失败: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("B 已停用, 不应返回启用计划: %+v", none)
	}
}

// TestIntegration_RecordPlanNotFoundBranches 不存在 / 空 patch 的分支.
func TestIntegration_RecordPlanNotFoundBranches(t *testing.T) {
	db := realVideoDB(t)
	m := NewRecordPlanModel(db)
	ctx := context.Background()

	if _, err := m.FindByID(ctx, planTestTenant, 999999999); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("期望 ErrRecordPlanNotFound, 实际 %v", err)
	}
	if err := m.Update(ctx, planTestTenant, 999999999, RecordPlanPatch{}); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("空 patch 或不存在都应返回 ErrRecordPlanNotFound, 实际 %v", err)
	}
	if err := m.Delete(ctx, planTestTenant, 999999999); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("删除不存在应返回 ErrRecordPlanNotFound, 实际 %v", err)
	}
}

// TestIntegration_RecordPlanTenantIsolation 跨租户不可见: RBAC 行级隔离的底线.
func TestIntegration_RecordPlanTenantIsolation(t *testing.T) {
	db := realVideoDB(t)
	m := NewRecordPlanModel(db)
	ctx := context.Background()
	cleanupPlans(t, m)
	t.Cleanup(func() { cleanupPlans(t, m) })

	p := newPlan(t, m, planTestCameraA, "集成-租户隔离")

	if _, err := m.FindByID(ctx, planTestTenant+1, p.ID); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("其它租户不应看到该计划, 实际 %v", err)
	}
	other := int8(RecordPlanStatusDisabled)
	if err := m.Update(ctx, planTestTenant+1, p.ID, RecordPlanPatch{Status: &other}); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("其它租户不应改到该计划, 实际 %v", err)
	}
	if err := m.Delete(ctx, planTestTenant+1, p.ID); !errors.Is(err, ErrRecordPlanNotFound) {
		t.Errorf("其它租户不应删到该计划, 实际 %v", err)
	}
}
