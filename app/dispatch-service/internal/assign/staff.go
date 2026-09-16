package assign

import (
	"context"

	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
)

// LoadStaffCandidates 从 dispatch_staff 载入**在岗且启用**的人员作为候选池.
//
// 这是首选池: 只有它带技能标签, 能支撑"技能优先"的打分。
// 返回空切片是正常情况(尚未录入人员), 由调用方回退到 LoadCandidates 的历史池,
// 保证功能上线前老行为不退化。
func LoadStaffCandidates(ctx context.Context, db *gorm.DB) ([]Candidate, error) {
	var staff []model.DispatchStaff
	if err := db.WithContext(ctx).
		Where("on_duty = ? AND status = ?", model.StaffOnDuty, model.StaffEnabled).
		Find(&staff).Error; err != nil {
		return nil, err
	}
	if len(staff) == 0 {
		return nil, nil
	}

	loads, err := loadByAssignee(ctx, db)
	if err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(staff))
	for i := range staff {
		s := &staff[i]
		out = append(out, Candidate{
			AssigneeId:   s.StaffId,
			AssigneeName: s.Name,
			ZoneCode:     s.ZoneCode,
			Skills:       s.SkillList(),
			Load:         loads[s.StaffId],
		})
	}
	return out, nil
}

// loadByAssignee 一次性取出各处理人当前在手工单数.
// 刻意用一次分组查询而不是逐个查询: 否则 20 个候选人就是 20 次 DB 往返。
func loadByAssignee(ctx context.Context, db *gorm.DB) (map[int64]int64, error) {
	var rows []struct {
		AssigneeId int64
		LoadCnt    int64
	}
	err := db.WithContext(ctx).
		Model(&model.DispatchTask{}).
		Select("assignee_id, COUNT(*) AS load_cnt").
		Where("assignee_id > 0 AND status IN ?",
			[]int8{model.StatusAssigned, model.StatusProcessing}).
		Group("assignee_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.AssigneeId] = r.LoadCnt
	}
	return out, nil
}
