package state

import (
	"testing"

	"onepark/app/dispatch-service/internal/model"
)

// 本文件把工单 FSM 的转移表整表锁死。
//
// 为什么值得专门测: 这张表是"什么操作能做"的唯一裁判 ——
// 有人往里加一条出边(或删一条)不会被任何编译器发现, 但工单可能因此永久卡住。
// 最典型的就是 ActionRelease: 没有它, 「已指派」工单超时后无处可去(见下方用例)。

// allActions 全部动作, 用于穷举"终态无出边".
var allActions = []string{
	ActionCreate, ActionAssign, ActionRelease, ActionStart, ActionFinish, ActionClose,
}

// TestNext_LegalTransitions 合法转移逐条固定.
func TestNext_LegalTransitions(t *testing.T) {
	cases := []struct {
		from   int8
		action string
		want   int8
	}{
		// 待指派
		{model.StatusPendingAssign, ActionAssign, model.StatusAssigned},
		{model.StatusPendingAssign, ActionClose, model.StatusClosed},
		// 已指派
		{model.StatusAssigned, ActionAssign, model.StatusAssigned},              // 改派(含超时自动重派) + 直接 start 之外
		{model.StatusAssigned, ActionRelease, model.StatusPendingAssign},       // ⭐ 释放回池(超时无人接管/达重派上限)
		{model.StatusAssigned, ActionStart, model.StatusProcessing},
		{model.StatusAssigned, ActionClose, model.StatusClosed},
		// 处理中
		{model.StatusProcessing, ActionFinish, model.StatusCompleted},
		{model.StatusProcessing, ActionClose, model.StatusClosed},
		// 已完成(仅可归档)
		{model.StatusCompleted, ActionClose, model.StatusClosed},
	}

	for _, c := range cases {
		got, ok := Next(c.from, c.action)
		if !ok {
			t.Errorf("Next(%d, %q) 被判非法, 期望合法 -> %d", c.from, c.action, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("Next(%d, %q) = %d, 期望 %d", c.from, c.action, got, c.want)
		}
	}
}

// TestNext_IllegalTransitions 非法转移必须被拒 —— 尤其是那些"看起来顺手"的.
func TestNext_IllegalTransitions(t *testing.T) {
	cases := []struct {
		from   int8
		action string
		why    string
	}{
		{model.StatusPendingAssign, ActionStart, "未指派不能直接开工"},
		{model.StatusPendingAssign, ActionFinish, "未指派不能直接完成"},
		{model.StatusPendingAssign, ActionRelease, "本就在池里, 释放无意义"},
		{model.StatusAssigned, ActionFinish, "必须先 start, 否则\"完成\"绕过开始时间统计"},
		{model.StatusProcessing, ActionAssign, "处理中不能被改派(等于抢走人正在干的活)"},
		{model.StatusProcessing, ActionRelease, "处理中不能释放回池"},
		{model.StatusCompleted, ActionAssign, "已完成不能再指派"},
		{model.StatusCompleted, ActionRelease, "已完成不能释放回池"},
		{model.StatusCompleted, ActionFinish, "不能重复完成"},
		{model.StatusClosed, ActionAssign, "已关闭是终态"},
		{model.StatusClosed, ActionRelease, "已关闭是终态"},
		{model.StatusClosed, ActionClose, "不能重复关闭"},
	}

	for _, c := range cases {
		if got, ok := Next(c.from, c.action); ok {
			t.Errorf("Next(%d, %q) 被判合法 -> %d, 期望非法(%s)", c.from, c.action, got, c.why)
		}
	}
}

// TestNext_TerminalHasNoOutEdge 终态「已关闭」对任何动作都无出边.
func TestNext_TerminalHasNoOutEdge(t *testing.T) {
	if !IsTerminal(model.StatusClosed) {
		t.Fatal("已关闭应为终态")
	}
	for _, a := range allActions {
		if to, ok := Next(model.StatusClosed, a); ok {
			t.Errorf("已关闭状态存在出边: action=%q -> %d, 终态不应可逆", a, to)
		}
	}
}

// TestNext_NonTerminalNotTerminal 除「已关闭」外都算非终态 —— 避免 IsTerminal 被误改成多值.
func TestNext_NonTerminalNotTerminal(t *testing.T) {
	for _, s := range []int8{
		model.StatusPendingAssign, model.StatusAssigned,
		model.StatusProcessing, model.StatusCompleted,
	} {
		if IsTerminal(s) {
			t.Errorf("状态 %d 不应被当作终态", s)
		}
	}
}

// TestIsValid 状态码合法性(防脏数据).
func TestIsValid(t *testing.T) {
	valid := []int8{
		model.StatusPendingAssign, model.StatusAssigned,
		model.StatusProcessing, model.StatusCompleted, model.StatusClosed,
	}
	for _, s := range valid {
		if !IsValid(s) {
			t.Errorf("状态 %d 应合法", s)
		}
	}
	for _, s := range []int8{0, 6, 99, -1} {
		if IsValid(s) {
			t.Errorf("状态 %d 不应合法", s)
		}
	}
}
