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

// TestPick 覆盖候选挑选: 就近优先, 同距离比负载, 空池返回 false.
func TestPick(t *testing.T) {
	tests := []struct {
		name       string
		targetZone string
		candidates []Candidate
		wantID     int64
		wantOK     bool
	}{
		{
			name:       "空候选池",
			targetZone: "A-3F-301",
			candidates: nil,
			wantOK:     false,
		},
		{
			name:       "就近优先于负载",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Pick(tt.targetZone, tt.candidates)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && got.AssigneeId != tt.wantID {
				t.Fatalf("AssigneeId = %d, 期望 %d", got.AssigneeId, tt.wantID)
			}
		})
	}
}
