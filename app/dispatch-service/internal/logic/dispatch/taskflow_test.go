package dispatch

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"

	"onepark/app/dispatch-service/internal/assign"
	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/gormx"
)

// 本文件覆盖调度工单的**业务主干**: 建单校验 / 状态流转 / 派单 / 人名回退。
// 此前本包 8 个源文件 **零测试** —— 之前只测了 cron 与 assign 两个旁支, 主干是空的。

// openTestDB 连本地 dispatch_db; 环境不可用时跳过(与 internal/cron 同一套约定).
func openTestDB(t *testing.T) *gormx.DB {
	t.Helper()

	for _, p := range []string{"../../../etc/dispatch-api.yaml", "../../../../etc/dispatch-api.yaml"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		var c config.Config
		// conf.UseEnv() 必须开: 配置里的 DSN/Redis 已是 ${VAR} 占位符(平台统一要求),
		// 不开则加载到的是字面量, 连接必然失败 -> DB 用例**静默跳过**(go test 仍打印 ok)。
		if err := conf.Load(p, &c, conf.UseEnv()); err != nil {
			continue
		}
		if c.MySQL.DataSource == "" {
			continue
		}
		db, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			continue
		}
		if err := sqlDB.Ping(); err != nil {
			continue
		}
		return db
	}

	t.Skip("跳过: 未找到可用的 etc/dispatch-api.yaml, 或本地 MySQL 不可用")
	return nil
}

// newTestTask 造一张工单并注册清理(含其审计流水).
func newTestTask(t *testing.T, db *gormx.DB, status int8, assigneeId int64) *model.DispatchTask {
	t.Helper()

	task := &model.DispatchTask{
		TaskNo:       fmt.Sprintf("DT-TEST-%d", time.Now().UnixNano()),
		Title:        "逻辑层测试工单",
		Source:       model.SourceManual,
		ZoneCode:     "Z-TEST-1F",
		Priority:     model.PriorityNormal,
		Status:       status,
		AssigneeId:   assigneeId,
		AssigneeName: fmt.Sprintf("处理人-%d", assigneeId),
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("准备工单失败: %v", err)
	}
	t.Cleanup(func() {
		db.Where("task_id = ?", task.Id).Delete(&model.DispatchTaskLog{})
		db.Delete(&model.DispatchTask{}, task.Id)
	})
	return task
}

// countTaskLogs 统计某工单的审计流水条数.
func countTaskLogs(t *testing.T, db *gormx.DB, taskId int64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.DispatchTaskLog{}).Where("task_id = ?", taskId).Count(&n).Error; err != nil {
		t.Fatalf("统计审计流水失败: %v", err)
	}
	return n
}

// ---------- 建单 ----------

// TestTaskCreate_Validation 建单入参校验逐条命中.
func TestTaskCreate_Validation(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	ctx := context.Background()
	logic := NewTaskCreateLogic(ctx, svcCtx)

	base := func() *types.TaskCreateReq {
		return &types.TaskCreateReq{
			Title: "校验测试", ZoneCode: "Z-TEST-1F", Priority: int32(model.PriorityNormal),
			Source: int32(model.SourceManual),
		}
	}

	cases := []struct {
		name   string
		mutate func(*types.TaskCreateReq)
	}{
		{"标题为空", func(r *types.TaskCreateReq) { r.Title = "" }},
		{"区域为空", func(r *types.TaskCreateReq) { r.ZoneCode = "" }},
		{"优先级为 0", func(r *types.TaskCreateReq) { r.Priority = 0 }},
		{"优先级为 4", func(r *types.TaskCreateReq) { r.Priority = 4 }},
		{"伪造告警来源", func(r *types.TaskCreateReq) { r.Source = int32(model.SourceAlarm) }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := base()
			c.mutate(req)
			if _, err := logic.TaskCreate(req); err == nil {
				t.Errorf("%s: 应被拒绝, 但通过了", c.name)
			}
		})
	}
}

// TestTaskCreate_NormalizesSkillAndForcesManualSource 建单会**归一化技能**并**强制 source=人工**.
//
// 强制 source 的意义: 告警来源的工单只允许 Kafka 消费者落库,
// 否则人工可以把 source 伪造成"告警"来绕过去重逻辑。
func TestTaskCreate_NormalizesSkillAndForcesManualSource(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()

	// 大小写混写 + 空格 + 重复, 都应该被抹平
	messy := "  Fire , fire ,  ELECTRICAL "
	resp, err := NewTaskCreateLogic(ctx, svcCtx).TaskCreate(&types.TaskCreateReq{
		Title: "归一化测试", ZoneCode: "Z-TEST-1F", Priority: int32(model.PriorityHigh),
		Source: int32(model.SourceManual), RequiredSkill: messy,
	})
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	t.Cleanup(func() {
		db.Where("task_id = ?", resp.Id).Delete(&model.DispatchTaskLog{})
		db.Delete(&model.DispatchTask{}, resp.Id)
	})

	var got model.DispatchTask
	if err := db.First(&got, resp.Id).Error; err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}

	if got.Status != model.StatusPendingAssign {
		t.Errorf("新工单状态 = %d, 期望待指派", got.Status)
	}
	if got.Source != model.SourceManual {
		t.Errorf("source = %d, 期望被强制为人工(%d)", got.Source, model.SourceManual)
	}
	if got.RequiredSkill != model.NormalizeSkills(messy) {
		t.Errorf("技能未按 model.NormalizeSkills 归一: got=%q want=%q", got.RequiredSkill, model.NormalizeSkills(messy))
	}
	// 具体性质: 不含空格、全小写、重复的 fire 只剩一个
	if strings.Contains(got.RequiredSkill, " ") {
		t.Errorf("归一后不应含空格: %q", got.RequiredSkill)
	}
	if got.RequiredSkill != strings.ToLower(got.RequiredSkill) {
		t.Errorf("归一后应全小写: %q", got.RequiredSkill)
	}
	if n := strings.Count(strings.ToLower(got.RequiredSkill), "fire"); n != 1 {
		t.Errorf("重复技能应去重, fire 出现 %d 次: %q", n, got.RequiredSkill)
	}
	// 建单必须留一条审计: 0 -> 待指派, action=create
	if n := countTaskLogs(t, db, resp.Id); n != 1 {
		t.Errorf("建单审计条数 = %d, 期望 1", n)
	}
}

// ---------- 状态流转 ----------

// TestTaskStatus_IllegalTransitions 非法转移必须被状态机拒绝.
func TestTaskStatus_IllegalTransitions(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskStatusLogic(ctx, svcCtx)

	cases := []struct {
		name    string
		status  int8
		actions []string
	}{
		{"待指派不能直接开工/完成", model.StatusPendingAssign, []string{state.ActionStart, state.ActionFinish}},
		{"已完成不能重开/再派/再完成", model.StatusCompleted, []string{state.ActionStart, state.ActionAssign, state.ActionFinish}},
		{"已关闭是终态", model.StatusClosed, []string{state.ActionStart, state.ActionFinish, state.ActionAssign, state.ActionClose}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := newTestTask(t, db, c.status, 0)
			for _, action := range c.actions {
				if _, err := logic.TaskStatus(&types.TaskStatusReq{Id: task.Id, Action: action}); err == nil {
					t.Errorf("状态 %d 下 action=%s 应被拒绝", c.status, action)
				}
			}
			// 被拒的请求不能留下审计
			if n := countTaskLogs(t, db, task.Id); n != 0 {
				t.Errorf("被拒绝的流转不应写审计, 实际 %d 条", n)
			}
		})
	}
}

// TestTaskStatus_FinishedAtOnlyOnTerminal 完成时间**只在终态写入**.
//
// 两个方向都要测: 不写(处理中却有了完成时间) 与 不写漏(完成后没有完成时间),
// 都是统计口径直接出错的问题。
func TestTaskStatus_FinishedAtOnlyOnTerminal(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskStatusLogic(ctx, svcCtx)

	task := newTestTask(t, db, model.StatusAssigned, 880001)

	// 已指派 -> 处理中: 不能有完成时间
	resp, err := logic.TaskStatus(&types.TaskStatusReq{Id: task.Id, Action: state.ActionStart, Remark: "开始处理"})
	if err != nil {
		t.Fatalf("开始处理失败: %v", err)
	}
	if resp.Status != int32(model.StatusProcessing) {
		t.Fatalf("状态 = %d, 期望处理中", resp.Status)
	}
	var mid model.DispatchTask
	if err := db.First(&mid, task.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if mid.FinishedAt != nil {
		t.Error("「处理中」不应写完成时间 —— 否则在途工单会被统计成已完成")
	}

	// 处理中 -> 完成: 必须有完成时间
	if _, err := logic.TaskStatus(&types.TaskStatusReq{Id: task.Id, Action: state.ActionFinish}); err != nil {
		t.Fatalf("完成失败: %v", err)
	}
	var done model.DispatchTask
	if err := db.First(&done, task.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if done.FinishedAt == nil {
		t.Error("「已完成」必须写完成时间")
	}

	// 审计: 2->3 与 3->4 各一条, action 正确
	var logs []model.DispatchTaskLog
	if err := db.Where("task_id = ?", task.Id).Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("审计条数 = %d, 期望 2", len(logs))
	}
	if logs[0].FromStatus != model.StatusAssigned || logs[0].ToStatus != model.StatusProcessing ||
		logs[0].Action != state.ActionStart {
		t.Errorf("第 1 条审计错误: from=%d to=%d action=%s", logs[0].FromStatus, logs[0].ToStatus, logs[0].Action)
	}
	if logs[1].FromStatus != model.StatusProcessing || logs[1].ToStatus != model.StatusCompleted ||
		logs[1].Action != state.ActionFinish {
		t.Errorf("第 2 条审计错误: from=%d to=%d action=%s", logs[1].FromStatus, logs[1].ToStatus, logs[1].Action)
	}
}

// TestTaskStatus_OptimisticLockPreventsDoubleTransition 并发流转只能成功一次.
//
// 为什么必须测: 状态更新带 `WHERE version = ?` 乐观锁, 若这层保护被去掉,
// 多个处理人同时点「开始处理」会**多次推进状态并写多条审计**, 版本号也会乱跳。
// 无锁时本用例的"成功次数"会 > 1, 直接暴露问题。
func TestTaskStatus_OptimisticLockPreventsDoubleTransition(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskStatusLogic(ctx, svcCtx)

	task := newTestTask(t, db, model.StatusAssigned, 880002)
	before := task.Version

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = logic.TaskStatus(&types.TaskStatusReq{Id: task.Id, Action: state.ActionStart})
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Errorf("并发 start 成功 %d 次, 期望恰好 1 次(乐观锁应拦住其余)", succeeded)
	}

	var got model.DispatchTask
	if err := db.First(&got, task.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.Status != model.StatusProcessing {
		t.Errorf("状态 = %d, 期望处理中(只应前进一次)", got.Status)
	}
	if got.Version != before+1 {
		t.Errorf("version = %d, 期望 %d(只应 +1 一次)", got.Version, before+1)
	}
	if n := countTaskLogs(t, db, task.Id); n != 1 {
		t.Errorf("审计条数 = %d, 期望 1(被拦下的请求不应留痕)", n)
	}
}

// TestTaskStatus_NotFound 不存在的工单.
func TestTaskStatus_NotFound(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	if _, err := NewTaskStatusLogic(context.Background(), svcCtx).
		TaskStatus(&types.TaskStatusReq{Id: 999_999_999, Action: state.ActionStart}); err == nil {
		t.Error("不存在的工单应报错")
	}
}

// ---------- 处理人姓名回退链 ----------

// TestLookupAssigneeName_FallbackChain 人名回退链: 人员池(权威) -> 历史指派记录 -> 空串.
//
// 最后一跳是重点: 无匹配行时 `MAX(assignee_name)` 返回 **NULL**,
// 若不 COALESCE 直接 Scan 到 string 会报错(源码注释里记录了这个坑)。
func TestLookupAssigneeName_FallbackChain(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskAssignLogic(ctx, svcCtx)

	staffId := 900_000 + time.Now().UnixNano()%100_000
	histName := fmt.Sprintf("处理人-%d", staffId)

	// 一张挂着该处理人的历史工单(用于第二跳)
	newTestTask(t, db, model.StatusCompleted, staffId)

	if err := db.Create(&model.DispatchStaff{
		StaffId: staffId, Name: "池中姓名", ZoneCode: "Z-TEST-1F",
		OnDuty: model.StaffOnDuty, Status: model.StaffEnabled,
	}).Error; err != nil {
		t.Fatalf("准备人员失败: %v", err)
	}
	t.Cleanup(func() { db.Where("staff_id = ?", staffId).Delete(&model.DispatchStaff{}) })

	// 第一跳: 人员池优先
	if got := logic.lookupAssigneeName(staffId); got != "池中姓名" {
		t.Errorf("人员池优先: got=%q, 期望 %q", got, "池中姓名")
	}

	// 第二跳: 池里没有 -> 回退历史工单
	db.Where("staff_id = ?", staffId).Delete(&model.DispatchStaff{})
	if got := logic.lookupAssigneeName(staffId); got != histName {
		t.Errorf("历史回退: got=%q, 期望 %q", got, histName)
	}

	// 第三跳: 哪儿都没有 -> 空串且不能报错(COALESCE 保护)
	if got := logic.lookupAssigneeName(999_999_997); got != "" {
		t.Errorf("查无此人应返回空串, 实际 %q", got)
	}

	// 非法 id 直接返回空串, 不查库
	if got := logic.lookupAssigneeName(0); got != "" {
		t.Errorf("assigneeId<=0 应返回空串, 实际 %q", got)
	}
}

// ---------- 指派 ----------

// TestTaskAssign_Manual 手动指派: 落处理人、重置接单窗口、写审计.
func TestTaskAssign_Manual(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskAssignLogic(ctx, svcCtx)

	task := newTestTask(t, db, model.StatusPendingAssign, 0)
	assigneeId := 900_000 + time.Now().UnixNano()%100_000

	if err := db.Create(&model.DispatchStaff{
		StaffId: assigneeId, Name: "手动指派对象", ZoneCode: "Z-TEST-1F",
		OnDuty: model.StaffOnDuty, Status: model.StaffEnabled,
	}).Error; err != nil {
		t.Fatalf("准备人员失败: %v", err)
	}
	t.Cleanup(func() { db.Where("staff_id = ?", assigneeId).Delete(&model.DispatchStaff{}) })

	resp, err := logic.TaskAssign(&types.TaskAssignReq{Id: task.Id, AssigneeId: assigneeId})
	if err != nil {
		t.Fatalf("指派失败: %v", err)
	}
	if resp.Status != int32(model.StatusAssigned) {
		t.Errorf("指派后状态 = %d, 期望已指派", resp.Status)
	}
	if resp.AssigneeName != "手动指派对象" {
		t.Errorf("处理人姓名 = %q, 期望从人员池反查得到", resp.AssigneeName)
	}

	var got model.DispatchTask
	if err := db.First(&got, task.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.AssigneeId != assigneeId {
		t.Errorf("assignee_id = %d, 期望 %d", got.AssigneeId, assigneeId)
	}
	// 接单窗口必须重置到"现在 + 5 分钟", 否则新处理人没有接单时限
	if got.AssignExpireAt == nil {
		t.Fatal("指派后必须有接单超时时间, 否则超时重派永远不会触发")
	}
	if d := time.Until(*got.AssignExpireAt); d < 4*time.Minute || d > 6*time.Minute {
		t.Errorf("接单窗口 = %v 之后, 期望约 5 分钟(model.AssignExpireWindow)", d)
	}
	if n := countTaskLogs(t, db, task.Id); n != 1 {
		t.Errorf("指派审计条数 = %d, 期望 1", n)
	}
}

// TestTaskAssign_IllegalStatus 非法状态下的指派(处理中不能改派 —— 那等于抢走人正在干的活).
func TestTaskAssign_IllegalStatus(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskAssignLogic(ctx, svcCtx)

	for _, status := range []int8{model.StatusProcessing, model.StatusCompleted, model.StatusClosed} {
		task := newTestTask(t, db, status, 880003)
		if _, err := logic.TaskAssign(&types.TaskAssignReq{Id: task.Id, AssigneeId: 880003}); err == nil {
			t.Errorf("状态 %d 下不应允许指派", status)
		}
	}
}

// TestTaskAssign_NoCandidate 自动指派但**人员池为空**时必须报错, 而不是派给一个空处理人.
//
// ⚠️ 这个分支在开发库里**构造不出来** —— 需要 dispatch_staff 与历史指派记录同时为空。
// 一开始老弟想用"独占技能标签"来造, 但那是错的: assign.Pick 里技能是**加分项(+10000)而非必要条件**,
// 没有该技能的人照样会被选中。所以这里先探测候选池, 非空就跳过。
// 「无人可派」的决策逻辑已由 internal/cron 的纯函数 decideReassign 用例确定性地覆盖。
func TestTaskAssign_NoCandidate(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskAssignLogic(ctx, svcCtx)

	candidates, _, err := assign.Pool(ctx, db)
	if err != nil {
		t.Fatalf("载入候选池失败: %v", err)
	}
	if len(candidates) > 0 {
		t.Skipf("本环境候选池非空(%d 人),「无人可派」分支无法构造", len(candidates))
	}

	task := newTestTask(t, db, model.StatusPendingAssign, 0)
	if _, err := logic.TaskAssign(&types.TaskAssignReq{Id: task.Id, AssigneeId: 0}); err == nil {
		t.Fatal("人员池为空时应报错, 而不是派给一个空处理人")
	}
	// 工单必须保持"待指派", 不能留下半指派状态
	var got model.DispatchTask
	if err := db.First(&got, task.Id).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.Status != model.StatusPendingAssign {
		t.Errorf("无人可派时状态应保持待指派, 实际 %d", got.Status)
	}
	if n := countTaskLogs(t, db, task.Id); n != 0 {
		t.Errorf("失败的指派不应写审计, 实际 %d 条", n)
	}
}
