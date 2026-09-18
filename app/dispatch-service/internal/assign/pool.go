package assign

import (
	"context"

	"gorm.io/gorm"
)

// 候选池来源标识。用于日志区分 —— 便于排查"为什么派出去的人没有技能信息"。
const (
	PoolStaff   = "staff"   // dispatch_staff: 带技能标签(首选)
	PoolHistory = "history" // 历史指派记录: 无技能标签(回退)
)

// Pool 载入候选处理人池: 优先 dispatch_staff(在岗且启用), 为空时回退历史指派记录.
//
// 为什么保留双轨: 技能池是后加的, 尚未录入人员时不能让自动指派退化成"无人可派"。
func Pool(ctx context.Context, db *gorm.DB) (candidates []Candidate, source string, err error) {
	staff, err := LoadStaffCandidates(ctx, db)
	if err != nil {
		return nil, "", err
	}
	if len(staff) > 0 {
		return staff, PoolStaff, nil
	}

	history, err := LoadCandidates(ctx, db)
	if err != nil {
		return nil, "", err
	}
	return history, PoolHistory, nil
}

// Without 剔除指定处理人.
//
// 用于「指派超时重派」: 必须排除刚超时的那一位, 否则会原样再派给他,
// 变成每一轮都重派同一个人的死循环。
func Without(candidates []Candidate, excludeId int64) []Candidate {
	if excludeId <= 0 {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.AssigneeId != excludeId {
			out = append(out, c)
		}
	}
	return out
}
