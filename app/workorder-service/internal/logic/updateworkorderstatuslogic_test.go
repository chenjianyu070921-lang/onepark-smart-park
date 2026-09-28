// updateworkorderstatuslogic_test.go 状态流转逻辑单元测试 —— 补漏优化任务「补 workorder-service 单元测试: 状态流转」.
// 范围: 不依赖 DB 的流转语义约束(主表+流水同事务、乐观锁由 m2-api-test.ps1 E2E 覆盖):
//   1. 终态写入完成时间: 已完成(3)/已关闭(4)需写 finished_at, 其余活跃态不写;
//   2. 动作→目标状态映射: submit/approve/reject/close 与 FSM 完全一致;
//   3. 非法动作(如待派单提交、待验收重复派单)必须被状态机前置拒绝.
package logic

import (
	"testing"

	"onepark/app/workorder-service/internal/state"
)

// TestUpdateStatusSetsFinishedAt 状态流转: 终态(已完成/已关闭)写入完成时间, 活跃态不写(口径自洽).
func TestUpdateStatusSetsFinishedAt(t *testing.T) {
	if !setsFinishedAt(state.StatusCompleted) {
		t.Error("已完成(3)应写入完成时间")
	}
	if !setsFinishedAt(state.StatusClosed) {
		t.Error("已关闭(4)应写入完成时间")
	}
	for _, s := range []int8{state.StatusPendingDispatch, state.StatusProcessing, state.StatusPendingVerify} {
		if setsFinishedAt(s) {
			t.Errorf("活跃态 %d 不应写入完成时间", s)
		}
	}
}

// TestUpdateStatus_ActionTransitions 状态流转动作集: submit/approve/reject/close 映射正确且与 FSM 一致.
func TestUpdateStatus_ActionTransitions(t *testing.T) {
	cases := []struct {
		from   int8
		action string
		to     int8
	}{
		{state.StatusProcessing, state.ActionSubmit, state.StatusPendingVerify},  // 提交→待验收
		{state.StatusPendingVerify, state.ActionApprove, state.StatusCompleted},  // 验收通过→已完成
		{state.StatusPendingVerify, state.ActionReject, state.StatusProcessing},  // 驳回→处理中
		{state.StatusPendingDispatch, state.ActionClose, state.StatusClosed},      // 取消→已关闭
		{state.StatusProcessing, state.ActionClose, state.StatusClosed},
		{state.StatusPendingVerify, state.ActionClose, state.StatusClosed},
		{state.StatusCompleted, state.ActionClose, state.StatusClosed},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			next, ok := state.NextStatus(c.from, c.action)
			if !ok {
				t.Fatalf("流转 %d --%s--> 应合法", c.from, c.action)
			}
			if next != c.to {
				t.Errorf("流转期望目标 %d, 实际 %d", c.to, next)
			}
		})
	}
	// 非法动作必须被状态机拒绝(状态流转前置校验口径).
	if _, ok := state.NextStatus(state.StatusPendingDispatch, state.ActionSubmit); ok {
		t.Error("待派单提交应被拒绝")
	}
	if _, ok := state.NextStatus(state.StatusPendingVerify, state.ActionAssign); ok {
		t.Error("待验收重复派单应被拒绝")
	}
}
