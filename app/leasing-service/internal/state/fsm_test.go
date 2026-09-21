package state

import (
	"testing"

	"onepark/app/leasing-service/internal/model"
)

// 本文件锁死合同 FSM 的转移表。
//
// 这张表有两处语义与 dispatch 的工单 FSM **刻意不同**(见下方对应用例):
//  1. 「已到期」**不是**终态 —— 仍可 terminate 做清退归档;
//  2. 「已终止」才是终态 —— 已终止合同不复活(重新出租请新建合同)。

// allActions 全部动作, 用于穷举"终态无出边".
var allActions = []string{ActionCreate, ActionActivate, ActionExpire, ActionTerminate, ActionRenew}

// TestNext_LegalTransitions 合法转移逐条固定.
func TestNext_LegalTransitions(t *testing.T) {
	cases := []struct {
		from   int8
		action string
		want   int8
		why    string
	}{
		{model.StatusPending, ActionActivate, model.StatusActive, "待生效 -> 生效中"},
		{model.StatusPending, ActionTerminate, model.StatusTerminated, "未生效即可终止(谈判破裂)"},

		{model.StatusActive, ActionRenew, model.StatusActive, "⭐ 生效中续签: 延长租期, 状态保持不变"},
		{model.StatusActive, ActionExpire, model.StatusExpired, "生效中 -> 已到期(自然到期)"},
		{model.StatusActive, ActionTerminate, model.StatusTerminated, "生效中 -> 已终止(提前解约)"},

		{model.StatusExpired, ActionRenew, model.StatusActive, "⭐ 到期后续签: 重新生效并延长租期"},
		{model.StatusExpired, ActionTerminate, model.StatusTerminated, "已到期 -> 已终止(清退归档)"},
	}

	for _, c := range cases {
		got, ok := Next(c.from, c.action)
		if !ok {
			t.Errorf("Next(%d, %q) 被判非法, 期望合法 -> %d(%s)", c.from, c.action, c.want, c.why)
			continue
		}
		if got != c.want {
			t.Errorf("Next(%d, %q) = %d, 期望 %d(%s)", c.from, c.action, got, c.want, c.why)
		}
	}
}

// TestNext_IllegalTransitions 非法转移必须被拒.
func TestNext_IllegalTransitions(t *testing.T) {
	cases := []struct {
		from   int8
		action string
		why    string
	}{
		{model.StatusPending, ActionExpire, "未生效谈何到期, 必须先 activate"},
		{model.StatusPending, ActionRenew, "⚠️ 见 TestPendingContractCannotChangeDates"},
		{model.StatusActive, ActionActivate, "已生效不能重复生效"},
		{model.StatusExpired, ActionActivate, "已到期不能靠 activate 复活, 应走 renew"},
		{model.StatusExpired, ActionExpire, "不能重复到期"},
		{model.StatusTerminated, ActionActivate, "已终止是终态, 不复活"},
		{model.StatusTerminated, ActionRenew, "已终止是终态, 不复活"},
		{model.StatusTerminated, ActionExpire, "已终止是终态"},
		{model.StatusTerminated, ActionTerminate, "不能重复终止"},
	}

	for _, c := range cases {
		if got, ok := Next(c.from, c.action); ok {
			t.Errorf("Next(%d, %q) 被判合法 -> %d, 期望非法(%s)", c.from, c.action, got, c.why)
		}
	}
}

// TestPendingContractCannotChangeDates 记录一个**已知的设计缺口**。
//
// 待生效合同的租期无法修改:
//   - renew   在「待生效」下非法(见上)
//   - update  只处理 monthly_rent / auto_renew / renew_notice_days, **不含起止日期**
//
// 也就是说: 合同录入后若发现日期写错, 在它生效之前**没有任何接口能改**。
// 本用例把当前行为钉住 —— 将来补上(允许待生效 renew 或 update 支持改期)时**必须同步改这里**,
// 而不是让它悄悄变化。
func TestPendingContractCannotChangeDates(t *testing.T) {
	if _, ok := Next(model.StatusPending, ActionRenew); ok {
		t.Fatalf("行为已变化: 待生效现在可以 renew 了。" +
			"若是刻意补上该能力, 请同步更新本用例与 docs/m5/04-招商租赁业务流程.md")
	}
}

// TestIsTerminal 到期**不是**终态, 终止才是 —— 与工单 FSM(已关闭才是终态)刻意不同.
func TestIsTerminal(t *testing.T) {
	if IsTerminal(model.StatusExpired) {
		t.Error("「已到期」不应是终态: 它仍可 terminate 做清退归档")
	}
	if !IsTerminal(model.StatusTerminated) {
		t.Error("「已终止」应为终态")
	}
	for _, s := range []int8{model.StatusPending, model.StatusActive} {
		if IsTerminal(s) {
			t.Errorf("状态 %d 不应被当作终态", s)
		}
	}
}

// TestNext_TerminalHasNoOutEdge 「已终止」对任何动作都无出边.
func TestNext_TerminalHasNoOutEdge(t *testing.T) {
	for _, a := range allActions {
		if to, ok := Next(model.StatusTerminated, a); ok {
			t.Errorf("已终止状态存在出边: action=%q -> %d, 终态不应可逆", a, to)
		}
	}
}

// TestIsValid 状态码合法性(防脏数据).
func TestIsValid(t *testing.T) {
	for _, s := range []int8{
		model.StatusPending, model.StatusActive, model.StatusExpired, model.StatusTerminated,
	} {
		if !IsValid(s) {
			t.Errorf("状态 %d 应合法", s)
		}
	}
	for _, s := range []int8{0, 5, 99, -1} {
		if IsValid(s) {
			t.Errorf("状态 %d 不应合法", s)
		}
	}
}
