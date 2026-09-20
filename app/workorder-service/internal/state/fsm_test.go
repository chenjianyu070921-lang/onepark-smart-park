// fsm_test.go 工单状态机(FSM)单元测试 —— 第五阶段任务「补 workorder-service 单元测试: 状态流转」.
// 覆盖口径(对齐 docs/M2 设计文档 V1.1 第八章):
//   1. 合法主路径: 建单→待派单(0), assign 0→1, submit 1→2, approve 2→3;
//   2. 分支路径: reject 2→1(驳回回处理中); close 从任意活跃态(0/1/2/3)→已关闭(4);
//   3. 非法流转: 终态(4)无任何出口; 状态与动作不匹配(如 0 直接 submit); 未知动作; 非法状态码;
//   4. 一致性: CanTransition 与 NextStatus 对全量(状态×动作)组合结论一致;
//   5. 辅助判定: IsTerminal 仅已关闭(4); IsValidStatus 仅认 0~4.
package state

import "testing"

// TestNextStatus_MainPath 主路径流转: 与设计文档状态图逐条对应.
func TestNextStatus_MainPath(t *testing.T) {
	cases := []struct {
		name string
		from int8
		act  string
		to   int8
	}{
		{"派单: 待派单→处理中", StatusPendingDispatch, ActionAssign, StatusProcessing},
		{"提交: 处理中→待验收", StatusProcessing, ActionSubmit, StatusPendingVerify},
		{"验收通过: 待验收→已完成", StatusPendingVerify, ActionApprove, StatusCompleted},
		{"驳回: 待验收→处理中", StatusPendingVerify, ActionReject, StatusProcessing},
		{"关闭: 待派单→已关闭", StatusPendingDispatch, ActionClose, StatusClosed},
		{"关闭: 处理中→已关闭", StatusProcessing, ActionClose, StatusClosed},
		{"关闭: 待验收→已关闭", StatusPendingVerify, ActionClose, StatusClosed},
		{"关闭: 已完成→已关闭", StatusCompleted, ActionClose, StatusClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			to, ok := NextStatus(c.from, c.act)
			if !ok {
				t.Fatalf("流转 %d --%s--> 应合法, 实际被拒绝", c.from, c.act)
			}
			if to != c.to {
				t.Fatalf("流转 %d --%s--> 期望 %d, 实际 %d", c.from, c.act, c.to, to)
			}
		})
	}
}

// TestNextStatus_Invalid 非法流转必须全部拒绝(防脏数据/越权状态跳变).
func TestNextStatus_Invalid(t *testing.T) {
	allActions := []string{ActionCreate, ActionAssign, ActionSubmit, ActionApprove, ActionReject, ActionClose}

	t.Run("终态已关闭无任何出口", func(t *testing.T) {
		for _, act := range allActions {
			if _, ok := NextStatus(StatusClosed, act); ok {
				t.Errorf("终态 %d 执行 %s 应被拒绝, 实际放行", StatusClosed, act)
			}
		}
	})

	t.Run("状态与动作不匹配", func(t *testing.T) {
		bad := []struct {
			from int8
			act  string
		}{
			{StatusPendingDispatch, ActionSubmit},  // 未派单不能提交
			{StatusPendingDispatch, ActionApprove}, // 未处理不能验收
			{StatusPendingDispatch, ActionReject},  // 未处理无从驳回
			{StatusProcessing, ActionAssign},       // 已派单不能重复派单
			{StatusProcessing, ActionApprove},      // 未提交不能直接验收
			{StatusPendingVerify, ActionAssign},    // 待验收不能回退派单
			{StatusPendingVerify, ActionSubmit},    // 待验收不能重复提交
			{StatusCompleted, ActionAssign},        // 已完成不能重新派单
			{StatusCompleted, ActionApprove},       // 已完成不能重复验收
		}
		for _, c := range bad {
			if _, ok := NextStatus(c.from, c.act); ok {
				t.Errorf("非法流转 %d --%s--> 应被拒绝, 实际放行", c.from, c.act)
			}
		}
	})

	t.Run("未知动作与非法状态码", func(t *testing.T) {
		invalidStatuses := []int8{-1, 5, 100}
		for _, s := range invalidStatuses {
			for _, act := range allActions {
				if _, ok := NextStatus(s, act); ok {
					t.Errorf("非法状态 %d 执行 %s 应被拒绝", s, act)
				}
			}
		}
		if _, ok := NextStatus(StatusPendingDispatch, "unknown"); ok {
			t.Error("未知动作 unknown 应被拒绝")
		}
		if _, ok := NextStatus(StatusPendingDispatch, ""); ok {
			t.Error("空动作应被拒绝")
		}
	})
}

// TestCanTransition_ConsistentWithNextStatus CanTransition 与 NextStatus 对全量组合结论必须一致
// (两个入口分别用于入参校验与目标状态计算, 结论分叉会导致校验与落库不一致).
func TestCanTransition_ConsistentWithNextStatus(t *testing.T) {
	statuses := []int8{0, 1, 2, 3, 4, -1, 5}
	actions := []string{ActionCreate, ActionAssign, ActionSubmit, ActionApprove, ActionReject, ActionClose, "unknown"}
	for _, s := range statuses {
		for _, act := range actions {
			_, nextOK := NextStatus(s, act)
			canOK := CanTransition(s, act)
			if nextOK != canOK {
				t.Errorf("状态 %d 动作 %s: CanTransition=%v 与 NextStatus=%v 结论不一致", s, act, canOK, nextOK)
			}
		}
	}
}

// TestIsTerminal 仅已关闭(4)为终态; 已完成(3)仍可 close, 故不是终态.
func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusClosed) {
		t.Error("已关闭(4)应为终态")
	}
	for _, s := range []int8{StatusPendingDispatch, StatusProcessing, StatusPendingVerify, StatusCompleted} {
		if IsTerminal(s) {
			t.Errorf("状态 %d 不应为终态", s)
		}
	}
}

// TestIsValidStatus 状态码合法性: 仅 0~4 合法(防脏数据/越权传参).
func TestIsValidStatus(t *testing.T) {
	for _, s := range []int8{0, 1, 2, 3, 4} {
		if !IsValidStatus(s) {
			t.Errorf("状态 %d 应合法", s)
		}
	}
	for _, s := range []int8{-1, 5, 6, 100} {
		if IsValidStatus(s) {
			t.Errorf("状态 %d 应不合法", s)
		}
	}
}

// TestCreateImpliesPendingDispatch 建单语义约束: create 动作不参与 FSM 流转
// (建单即待派单, 由 buildCreateFlow 落 from=-1/to=0 流水, FSM 表中不应存在 create 出边).
func TestCreateImpliesPendingDispatch(t *testing.T) {
	for _, s := range []int8{StatusPendingDispatch, StatusProcessing, StatusPendingVerify, StatusCompleted, StatusClosed} {
		if _, ok := NextStatus(s, ActionCreate); ok {
			t.Errorf("create 不应作为 FSM 流转动作(状态 %d), 建单直接落待派单", s)
		}
	}
}
