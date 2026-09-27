package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/ctxdata"
)

// 本文件覆盖 2026-09-27 为前端答辩演示补的两项能力 + 一处顺带修的写入缺口:
//  1. 工单列表支持按园区(zone_code)过滤
//  2. 工单详情返回状态流转时间线(logs)
//  3. 审计流水补写租户 —— 原先 5 个写入点只有 1 个写了 tenant_id

// seedTask 造一张工单(纳秒后缀保证不与库里既有数据冲突).
func seedTask(t *testing.T, svcCtx *svc.ServiceContext, tenantID int64, zone string) *model.DispatchTask {
	t.Helper()
	suffix := time.Now().UnixNano() % 1_000_000_000
	task := &model.DispatchTask{
		TaskNo:    fmt.Sprintf("DT-TEST-%d-%s", suffix, zone),
		Title:     "详情/列表测试工单",
		Source:    model.SourceManual,
		TenantID:  tenantID,
		ZoneCode:  zone,
		Priority:  model.PriorityNormal,
		Status:    model.StatusPendingAssign,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := svcCtx.DB.WithContext(context.Background()).Create(task).Error; err != nil {
		t.Fatalf("准备工单失败: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		svcCtx.DB.WithContext(bg).Where("task_id = ?", task.Id).Delete(&model.DispatchTaskLog{})
		svcCtx.DB.WithContext(bg).Delete(&model.DispatchTask{}, task.Id)
	})
	return task
}

// TestTaskList_FilterByZoneCode 列表必须支持按园区精确过滤, 且**不影响未传该参数时的口径**。
func TestTaskList_FilterByZoneCode(t *testing.T) {
	db := openTestDB(t)
	tenantID := time.Now().UnixNano() % 100_000_000
	svcCtx := &svc.ServiceContext{DB: db}

	zoneA := "Z-A-101"
	zoneB := "Z-B-202"
	seedTask(t, svcCtx, tenantID, zoneA)
	seedTask(t, svcCtx, tenantID, zoneB)

	ctx := ctxdata.SetTenantId(context.Background(), tenantID)
	l := NewTaskListLogic(ctx, svcCtx)

	// 1) 只筛 zoneA: 恰好命中 zoneA 那一张
	resp, err := l.TaskList(&types.TaskListReq{ZoneCode: zoneA})
	if err != nil {
		t.Fatalf("TaskList(zoneA) 失败: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("按 zoneA 过滤命中 %d 条, 期望 1", resp.Total)
	}
	for _, it := range resp.List {
		if it.ZoneCode != zoneA {
			t.Errorf("过滤后出现了其它园区: %s", it.ZoneCode)
		}
	}

	// 2) 不传 zone_code: 两张都要在(别把新参数做成"不传就查不到")
	all, err := l.TaskList(&types.TaskListReq{})
	if err != nil {
		t.Fatalf("TaskList(无过滤) 失败: %v", err)
	}
	if all.Total < 2 {
		t.Errorf("不传 zone_code 时命中 %d 条, 期望 >= 2(新参数不能改变默认口径)", all.Total)
	}
}

// TestTaskDetail_ReturnsTimeline 详情必须返回**按发生顺序**的状态流转时间线。
func TestTaskDetail_ReturnsTimeline(t *testing.T) {
	db := openTestDB(t)
	tenantID := time.Now().UnixNano() % 100_000_000
	svcCtx := &svc.ServiceContext{DB: db}
	task := seedTask(t, svcCtx, tenantID, "Z-T-303")

	// 造三段流转: create -> assign -> release(顺序即 id 顺序)
	for _, s := range []struct {
		from, to int8
		action   string
	}{
		{0, model.StatusPendingAssign, "create"},
		{model.StatusPendingAssign, model.StatusAssigned, "assign"},
		{model.StatusAssigned, model.StatusPendingAssign, "release"},
	} {
		if err := db.WithContext(context.Background()).Create(&model.DispatchTaskLog{
			TenantID: tenantID, TaskId: task.Id,
			FromStatus: s.from, ToStatus: s.to, Action: s.action,
		}).Error; err != nil {
			t.Fatalf("准备审计失败: %v", err)
		}
	}

	ctx := ctxdata.SetTenantId(context.Background(), tenantID)
	detail, err := NewTaskDetailLogic(ctx, svcCtx).TaskDetail(&types.TaskDetailReq{Id: task.Id})
	if err != nil {
		t.Fatalf("TaskDetail 失败: %v", err)
	}
	if len(detail.Logs) != 3 {
		t.Fatalf("时间线 %d 条, 期望 3: %+v", len(detail.Logs), detail.Logs)
	}
	want := []string{"create", "assign", "release"}
	for i, w := range want {
		if detail.Logs[i].Action != w {
			t.Errorf("第 %d 格 action = %s, 期望 %s(时间线必须按发生顺序)", i, detail.Logs[i].Action, w)
		}
		if detail.Logs[i].Id == 0 {
			t.Errorf("第 %d 格缺少 id", i)
		}
	}
	// 建单那一格的起点是 0(不存在), 这是时间线的"第一帧"
	if detail.Logs[0].FromStatus != 0 {
		t.Errorf("首格 from_status = %d, 期望 0", detail.Logs[0].FromStatus)
	}
}

// TestTaskCreate_WritesAuditTenant 建单路径的审计必须带上租户.
//
// 原先 5 个写审计的地方只有「状态回写」一处写了 tenant_id, 其余 4 处(建单/派单/重派/
// 告警自动建单)都是 0 —— 一旦按租户查流水就会整段查不到。本用例盯住建单这一处。
func TestTaskCreate_WritesAuditTenant(t *testing.T) {
	db := openTestDB(t)
	tenantID := time.Now().UnixNano() % 100_000_000
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := ctxdata.SetTenantId(context.Background(), tenantID)

	resp, err := NewTaskCreateLogic(ctx, svcCtx).TaskCreate(&types.TaskCreateReq{
		Title: "审计租户测试", Source: 1, ZoneCode: "Z-C-404", Priority: 3,
	})
	if err != nil {
		t.Fatalf("TaskCreate 失败: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		db.WithContext(bg).Where("task_id = ?", resp.Id).Delete(&model.DispatchTaskLog{})
		db.WithContext(bg).Delete(&model.DispatchTask{}, resp.Id)
	})

	var logs []model.DispatchTaskLog
	if err := db.WithContext(context.Background()).
		Where("task_id = ?", resp.Id).Find(&logs).Error; err != nil {
		t.Fatalf("查询审计失败: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("建单应恰好写 1 条审计, 实际 %d", len(logs))
	}
	if logs[0].TenantID != tenantID {
		t.Errorf("审计 tenant_id = %d, 期望 %d(不写租户会让流水按租户查不到)", logs[0].TenantID, tenantID)
	}
	if logs[0].Action != "create" {
		t.Errorf("审计 action = %s, 期望 create", logs[0].Action)
	}
}
