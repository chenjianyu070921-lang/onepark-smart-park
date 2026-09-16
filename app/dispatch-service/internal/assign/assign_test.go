package assign

import "testing"

// TestZoneDistance 覆盖拓扑距离的各个分支(对称操作 + 边界输入).
func TestZoneDistance(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"同一区域", "A-3F-301", "A-3F-301", 0},
		{"同层不同房间", "A-3F-301", "A-3F-308", 1},
		{"同栋不同层", "A-3F-301", "A-5F-501", 2},
		{"跨楼栋", "A-3F-301", "B-2F-201", 3},
		{"目标编码为空", "", "A-3F", 3},
		{"双方编码为空", "", "", 3},
		{"带首尾斜杠", "/A-3F-301/", "A-3F-302", 1},
		{"只有楼栋段", "A", "A", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ZoneDistance(tt.a, tt.b); got != tt.want {
				t.Fatalf("ZoneDistance(%q, %q) = %d, 期望 %d", tt.a, tt.b, got, tt.want)
			}
			// 距离必须对称
			if got := ZoneDistance(tt.b, tt.a); got != tt.want {
				t.Fatalf("ZoneDistance(%q, %q) 非对称: %d, 期望 %d", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

// TestPick 覆盖候选挑选: 技能 > 就近 > 负载, 以及空池/技能无人匹配的兜底.
func TestPick(t *testing.T) {
	tests := []struct {
		name          string
		targetZone    string
		requiredSkill string
		candidates    []Candidate
		wantID        int64
		wantOK        bool
	}{
		{
			name:       "空候选池",
			targetZone: "A-3F-301",
			candidates: nil,
			wantOK:     false,
		},
		{
			name:       "未指定技能时就近优先于负载",
			targetZone: "A-3F-301",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "B-2F-201", Load: 0},
				{AssigneeId: 2, ZoneCode: "A-3F-302", Load: 5},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			name:       "距离相同比负载",
			targetZone: "A-3F-301",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "A-3F-305", Load: 3},
				{AssigneeId: 2, ZoneCode: "A-3F-306", Load: 1},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			name:       "完全同分取先出现者",
			targetZone: "A-3F-301",
			candidates: []Candidate{
				{AssigneeId: 7, ZoneCode: "A-3F-302", Load: 2},
				{AssigneeId: 8, ZoneCode: "A-3F-303", Load: 2},
			},
			wantID: 7,
			wantOK: true,
		},
		{
			// 技能权重压倒距离: 会干活的人哪怕跨楼栋, 也优先于近处不会干的人
			name:          "技能压过距离(跨楼栋但有技能者胜出)",
			targetZone:    "A-3F-301",
			requiredSkill: "fire",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "A-3F-302", Load: 0},                          // 就在隔壁, 但没技能
				{AssigneeId: 2, ZoneCode: "B-2F-201", Load: 9, Skills: []string{"fire"}}, // 跨楼栋, 有技能
			},
			wantID: 2,
			wantOK: true,
		},
		{
			// 技能权重同时压过负载: 有技能的人再忙也优先, 因为"能不能干"是硬条件
			name:          "技能压过负载",
			targetZone:    "A-3F-301",
			requiredSkill: "electrical",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "A-3F-302", Load: 0},
				{AssigneeId: 2, ZoneCode: "A-3F-302", Load: 8, Skills: []string{"electrical"}},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			// 技能匹配者之间继续比距离
			name:          "都有技能时比距离",
			targetZone:    "A-3F-301",
			requiredSkill: "security",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "B-1F-101", Load: 0, Skills: []string{"security"}},
				{AssigneeId: 2, ZoneCode: "A-3F-309", Load: 3, Skills: []string{"security"}},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			// 关键兜底: 没人有该技能时不能把工单卡住, 要退到就近+负载
			name:          "无人匹配技能时退化为就近+负载",
			targetZone:    "A-3F-301",
			requiredSkill: "plumbing",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "B-2F-201", Load: 0, Skills: []string{"fire"}},
				{AssigneeId: 2, ZoneCode: "A-3F-302", Load: 7, Skills: []string{"security"}},
			},
			wantID: 2,
			wantOK: true,
		},
		{
			// 大小写与空白不应影响匹配
			name:          "技能匹配忽略大小写与空格",
			targetZone:    "A-3F-301",
			requiredSkill: "Fire",
			candidates: []Candidate{
				{AssigneeId: 1, ZoneCode: "A-3F-302", Skills: []string{"electrical"}},
				{AssigneeId: 2, ZoneCode: "B-1F-101", Skills: []string{" fire "}},
			},
			wantID: 2,
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Pick(tt.targetZone, tt.requiredSkill, tt.candidates)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && got.AssigneeId != tt.wantID {
				t.Fatalf("AssigneeId = %d, 期望 %d", got.AssigneeId, tt.wantID)
			}
		})
	}
}

// TestExplain_LexicographicPrecedence 锁定三层优先级的**字典序**关系:
// 权重设计(10000 / 100 / 99)保证技能差一档一定压过距离差一档, 距离差一档一定压过负载差一档。
// 若日后有人调权重打破这个关系, 本测试会失败 —— 这正是它存在的意义。
func TestExplain_LexicographicPrecedence(t *testing.T) {
	// 距得最远的有技能者
	withSkill := Candidate{AssigneeId: 1, ZoneCode: "Z-9F-999", Load: 0, Skills: []string{"fire"}}
	// 距得最近的无技能者
	withoutSkill := Candidate{AssigneeId: 2, ZoneCode: "A-3F-301", Load: 0}

	s1 := Explain("A-3F-301", "fire", withSkill).Score
	s2 := Explain("A-3F-301", "fire", withoutSkill).Score
	if s1 <= s2 {
		t.Errorf("技能应压过距离与负载: 有技能=%d, 无技能=%d", s1, s2)
	}

	// 超大负载也不能翻过距离一档(负载项被截断在 99)
	nearBusy := Explain("A-3F-301", "", Candidate{AssigneeId: 3, ZoneCode: "A-3F-301", Load: 100000})
	farIdle := Explain("A-3F-301", "", Candidate{AssigneeId: 4, ZoneCode: "Z-9F-999", Load: 0})
	if nearBusy.Score <= farIdle.Score {
		t.Errorf("距离应压过负载(负载需被截断): 近处繁忙=%d, 远处空闲=%d", nearBusy.Score, farIdle.Score)
	}
}

// TestCandidate_HasSkill 覆盖技能匹配的边界.
func TestCandidate_HasSkill(t *testing.T) {
	c := Candidate{Skills: []string{"fire", "electrical"}}

	if !c.HasSkill("fire") {
		t.Error("应命中 fire")
	}
	if !c.HasSkill("FIRE") {
		t.Error("技能匹配应忽略大小写")
	}
	if !c.HasSkill(" electrical ") {
		t.Error("技能匹配应忽略首尾空白")
	}
	if c.HasSkill("security") {
		t.Error("不应命中未持有的技能")
	}
	if c.HasSkill("") {
		t.Error("空技能不应命中任何人")
	}
}
