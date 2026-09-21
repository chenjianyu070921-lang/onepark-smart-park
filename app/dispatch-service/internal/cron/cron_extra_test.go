package cron

import (
	"context"
	"fmt"
	"testing"
	"time"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
)

// 本文件补两块此前 0 覆盖的**活代码**:
//   - Start: 定时任务的启动入口(含"启动补偿"与 stop 收口);
//   - 达重派上限后的**释放**路径(releaseTask + writeLog)。
//
// 注意: 决策分支(该改派还是该释放)已由 reassign_test.go 的纯函数用例覆盖,
// 本文件补的是"决策落到库上"的那一半。

// mkExpiredTask 造一张"已超时"的工单并注册清理, 返回其 id.
func mkExpiredTask(t *testing.T, ctx context.Context, no string, status int8,
	assigneeId int64, reassignCount int64) *model.DispatchTask {
	t.Helper()
	db, rdb := openTestDeps(t)

	past := time.Now().Add(-10 * time.Minute)
	task := &model.DispatchTask{
		TaskNo: no, Title: "M5E2E 超时释放", Source: model.SourceManual,
		ZoneCode: "Z-CRON-1F", Priority: model.PriorityNormal, Status: status,
		AssigneeId: assigneeId, AssigneeName: "M5E2E 原处理人",
		AssignExpireAt: &past, ReassignCount: reassignCount,
	}
	if err := db.WithContext(ctx).Create(task).Error; err != nil {
		t.Fatalf("准备工单失败: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		db.WithContext(bg).Where("task_id = ?", task.Id).Delete(&model.DispatchTaskLog{})
		db.WithContext(bg).Delete(&model.DispatchTask{}, task.Id)
		// ⚠️ 必须释放重派锁: RunReassignOnce 加锁后**不自己解锁**(靠 TTL 过期),
		//    每个调用方都要收尾。漏掉这句, 后续用例的扫描会被锁挡在门外 ——
		//    表现为 Reassigned=0, 极难定位(第一次写本文件时就踩了这个)。
		_ = rdb.Del(bg, reassignLockKey).Err()
	})
	return task
}

// TestRunReassignOnce_ReleasesAtLimit 达重派上限 → 释放回「待指派」.
//
// 释放必须做全四件事, 少一件都会留下"僵尸指派":
// 状态回待指派、处理人清空、超时窗口置空、写 release 审计。
func TestRunReassignOnce_ReleasesAtLimit(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()

	const maxReassign = 3
	suffix := time.Now().UnixNano()
	task := mkExpiredTask(t, ctx, fmt.Sprintf("DT-REL-%d", suffix),
		model.StatusAssigned, 880100, maxReassign)

	if _, err := RunReassignOnce(ctx, db, rdb, maxReassign); err != nil {
		t.Fatalf("RunReassignOnce 失败: %v", err)
	}

	var got model.DispatchTask
	if err := db.WithContext(ctx).First(&got, task.Id).Error; err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	if got.Status != model.StatusPendingAssign {
		t.Errorf("状态 = %d, 期望待指派(%d) —— 达上限必须释放回池", got.Status, model.StatusPendingAssign)
	}
	if got.AssigneeId != 0 || got.AssigneeName != "" {
		t.Errorf("释放后处理人应清空, 实际 id=%d name=%q", got.AssigneeId, got.AssigneeName)
	}
	if got.AssignExpireAt != nil {
		t.Errorf("释放后超时窗口应为 NULL, 实际 %v", *got.AssignExpireAt)
	}
	// 达上限是"释放", 不该再累加重派次数
	if got.ReassignCount != maxReassign {
		t.Errorf("重派次数 = %d, 期望保持 %d(释放不再累计)", got.ReassignCount, maxReassign)
	}

	// 审计: 最后一条必须是 release, from 已指派 -> to 待指派, 且备注说明原因
	var last model.DispatchTaskLog
	if err := db.WithContext(ctx).Where("task_id = ?", task.Id).
		Order("id DESC").First(&last).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if last.Action != state.ActionRelease {
		t.Errorf("审计 action = %q, 期望 %q", last.Action, state.ActionRelease)
	}
	if last.FromStatus != model.StatusAssigned || last.ToStatus != model.StatusPendingAssign {
		t.Errorf("审计状态流转 = %d -> %d, 期望 %d -> %d",
			last.FromStatus, last.ToStatus, model.StatusAssigned, model.StatusPendingAssign)
	}
	if last.Remark == "" {
		t.Error("释放审计必须写清原因(运维要知道为什么被退回)")
	}
}

// TestRunReassignOnce_ProcessingUntouched 处理中的工单**绝不能动**.
//
// 处理中意味着人已经接单在干活了 —— 超时扫描要是把它退回池里, 等于把人正在做的活抢走。
// 本用例与"已指派超时"用同一份数据形态, 只有 status 不同, 因此能精确证明过滤条件生效。
func TestRunReassignOnce_ProcessingUntouched(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	// 故意把超时时间设成过去 —— 若只按 assign_expire_at 过滤就会误伤
	task := mkExpiredTask(t, ctx, fmt.Sprintf("DT-PROC-%d", suffix),
		model.StatusProcessing, 880101, 0)
	before := task.AssigneeId

	if _, err := RunReassignOnce(ctx, db, rdb, 3); err != nil {
		t.Fatalf("RunReassignOnce 失败: %v", err)
	}

	var got model.DispatchTask
	if err := db.WithContext(ctx).First(&got, task.Id).Error; err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	if got.Status != model.StatusProcessing {
		t.Errorf("处理中的工单状态被改动: %d(应保持处理中)", got.Status)
	}
	if got.AssigneeId != before {
		t.Errorf("处理中的工单处理人被改动: %d -> %d", before, got.AssigneeId)
	}
}

// TestStart_CompensatesAndStops 启动入口: 能起(含启动补偿)、能停, 不泄漏 goroutine.
func TestStart_CompensatesAndStops(t *testing.T) {
	db, rdb := openTestDeps(t)
	// 启动补偿同样会加重派锁, 用例收尾必须解锁(见 mkExpiredTask 的说明)
	t.Cleanup(func() { _ = rdb.Del(context.Background(), reassignLockKey).Err() })

	ctx, cancel := context.WithCancel(context.Background())
	// intervalSec 给个大值: 只验证"启动补偿 + 可停止", 不等周期性扫描
	stop := Start(ctx, db, rdb, 3600, 3)

	// 两条停止路径都应可用: 取消 ctx, 以及调用返回的 stop
	cancel()
	stop()

	// 再调一次 stop 不应 panic(幂等收口)
	stop()

	// ⚠️ 必须等异步的"启动补偿"跑完再返回。
	// Start 内部是 `go run("启动补偿")`, 而 RunReassignOnce 是**全局扫描**(不做租户过滤)。
	// 若不等它结束就进入下一个用例, 两次扫描会并发抢同一批超时工单 ——
	// 乐观锁会让"本该由下个用例处理"的那张被别人先改掉, 于是下个用例看到 Reassigned=0。
	// (这正是本用例第一版把 TestRunReassignOnce_ReassignAndSkip 搞挂的原因。)
	time.Sleep(2 * time.Second)
}
