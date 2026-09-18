package assign

import "testing"

// TestWithout 覆盖候选排除 —— 这是「超时重派」不变成死循环的关键。
func TestWithout(t *testing.T) {
	base := []Candidate{
		{AssigneeId: 1, AssigneeName: "A"},
		{AssigneeId: 2, AssigneeName: "B"},
		{AssigneeId: 3, AssigneeName: "C"},
	}

	t.Run("剔除中间一个", func(t *testing.T) {
		got := Without(base, 2)
		if len(got) != 2 {
			t.Fatalf("长度 = %d, 期望 2", len(got))
		}
		for _, c := range got {
			if c.AssigneeId == 2 {
				t.Error("被剔除的候选人仍在结果中")
			}
		}
	})

	t.Run("excludeId<=0 时原样返回", func(t *testing.T) {
		if got := Without(base, 0); len(got) != len(base) {
			t.Errorf("excludeId=0 不应剔除任何人: 长度 = %d", len(got))
		}
		if got := Without(base, -1); len(got) != len(base) {
			t.Errorf("excludeId<0 不应剔除任何人: 长度 = %d", len(got))
		}
	})

	t.Run("剔除不存在的ID返回全量", func(t *testing.T) {
		if got := Without(base, 999); len(got) != len(base) {
			t.Errorf("长度 = %d, 期望 %d", len(got), len(base))
		}
	})

	t.Run("全部剔除", func(t *testing.T) {
		// 只把原处理人放进池, 排除后应为空 -> decideReassign 会走「释放」分支
		only := []Candidate{{AssigneeId: 7, AssigneeName: "Solo"}}
		if got := Without(only, 7); len(got) != 0 {
			t.Errorf("长度 = %d, 期望 0", len(got))
		}
	})

	t.Run("不改动入参切片", func(t *testing.T) {
		_ = Without(base, 1)
		if len(base) != 3 {
			t.Errorf("原切片被修改: 长度 = %d", len(base))
		}
	})
}
