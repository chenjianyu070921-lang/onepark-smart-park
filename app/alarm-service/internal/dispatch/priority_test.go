package dispatch

import (
	"testing"

	"onepark/app/alarm-service/internal/model"
)

// TestDefaultTable_SeverityMapsToFewerBuckets 三个等级的换算方向与 M5 一致:
// M3 等级越大越严重, M5 优先级越小越紧急 —— 两者方向相反, 必须单调递减(非增).
//
// 这条用例锁的是"方向搞反"这一类错误: 若哪天有人把映射改成透传(level → priority),
// 严重告警会以"普通"优先级派出去, 而表面上看代码依旧自洽、单测除了这条也不会报错.
func TestDefaultTable_SeverityMapsToFewerBuckets(t *testing.T) {
	cases := []struct {
		level int8
		want  int8
	}{
		{model.AlarmLevelCritical, PriorityUrgent},
		{model.AlarmLevelMajor, PriorityHigh},
		{model.AlarmLevelMinor, PriorityNormal},
		{model.AlarmLevelInfo, PriorityNormal},
	}
	for _, c := range cases {
		if got := DefaultTable.PriorityOf(c.level); got != c.want {
			t.Errorf("等级 %d 应映射到优先级 %d, 实际 %d", c.level, c.want, got)
		}
	}

	prev := DefaultTable.PriorityOf(model.AlarmLevelCritical)
	for _, level := range []int8{model.AlarmLevelMajor, model.AlarmLevelMinor, model.AlarmLevelInfo} {
		got := DefaultTable.PriorityOf(level)
		if got < prev {
			t.Errorf("等级降低时优先级数值不应变小(越小越紧急): level=%d got=%d prev=%d", level, got, prev)
		}
		prev = got
	}
}

// TestTable_PriorityOfUnknownLevelFallsBackToNormal 未知等级按普通处理, 且不 panic.
//
// 为什么不是返回错误: M3 没有"低优先级"这一概念之外的第四档,
// 让异常取值在源头被丢弃会变成"告警存在但没人被派去处理"的漏派单.
func TestTable_PriorityOfUnknownLevelFallsBackToNormal(t *testing.T) {
	var nilTable Table
	for _, level := range []int8{0, 5, -1, 127} {
		if got := nilTable.PriorityOf(level); got != PriorityNormal {
			t.Errorf("nil 表 + 未知等级 %d 应回退 %d, 实际 %d", level, PriorityNormal, got)
		}
	}
}

// TestParseTable_OverridesMergeOnDefault 覆盖项只改动指定等级, 其余保持默认.
func TestParseTable_OverridesMergeOnDefault(t *testing.T) {
	table, rejected := ParseTable(map[string]int{"3": 1})
	if len(rejected) != 0 {
		t.Fatalf("合法覆盖项不应被拒绝: %v", rejected)
	}
	if got := table.PriorityOf(model.AlarmLevelMajor); got != PriorityUrgent {
		t.Errorf("被覆盖的等级3应变为 %d, 实际 %d", PriorityUrgent, got)
	}
	// 未覆盖的等级必须保持默认, 否则"改一条"会变成"整表失效".
	if got := table.PriorityOf(model.AlarmLevelCritical); got != PriorityUrgent {
		t.Errorf("未覆盖的等级4应保持默认 %d, 实际 %d", PriorityUrgent, got)
	}
	if got := table.PriorityOf(model.AlarmLevelMinor); got != PriorityNormal {
		t.Errorf("未覆盖的等级2应保持默认 %d, 实际 %d", PriorityNormal, got)
	}
}

// TestParseTable_RejectsIllegalEntries 非法条目逐条返回原因, 由调用方记 WARN:
// 静默忽略会让"配置写错"表现为"一直按老规则派单", 现场无从分辨.
func TestParseTable_RejectsIllegalEntries(t *testing.T) {
	cases := map[string]map[string]int{
		"等级越界":   {"9": 1},
		"等级为0":   {"0": 2},
		"等级非数字":  {"critical": 1},
		"优先级越界":  {"4": 9},
		"优先级为0":  {"4": 0},
	}
	for name, overrides := range cases {
		table, rejected := ParseTable(overrides)
		if len(rejected) != 1 {
			t.Errorf("%s: 应返回 1 条拒绝原因, 实际 %v", name, rejected)
			continue
		}
		if len(table) != len(DefaultTable) {
			t.Errorf("%s: 被拒绝后映射表规模不应变化", name)
		}
	}
}

// TestParseTable_NilOverridesKeepsDefault 未配置时返回等价默认的完整映射.
func TestParseTable_NilOverridesKeepsDefault(t *testing.T) {
	table, rejected := ParseTable(nil)
	if len(rejected) != 0 {
		t.Fatalf("未配置时不应有拒绝项: %v", rejected)
	}
	for level, want := range DefaultTable {
		if got := table[level]; got != want {
			t.Errorf("等级 %d 应为 %d, 实际 %d", level, want, got)
		}
	}
}

// TestParseTable_DoesNotMutateDefaultTable 解析结果必须是副本:
// 否则覆盖会写进包级 DefaultTable, 其它共用默认表的组件(含其它测试用例)被污染.
func TestParseTable_DoesNotMutateDefaultTable(t *testing.T) {
	ParseTable(map[string]int{"3": 1})
	if DefaultTable[model.AlarmLevelMajor] != PriorityHigh {
		t.Fatalf("解析覆盖项污染了默认映射: %v", DefaultTable)
	}
}
