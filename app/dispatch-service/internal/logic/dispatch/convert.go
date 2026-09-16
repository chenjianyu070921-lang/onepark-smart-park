package dispatch

import (
	"time"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/types"
)

// assignExpireWindow 指派超时窗口: 超过该时间未接单, 由定时任务重派(P2).
const assignExpireWindow = 5 * time.Minute

// maxPageSize 单页上限.
const maxPageSize = 200

// toTaskDTO 将数据模型转换为对外契约对象.
func toTaskDTO(m *model.DispatchTask) types.DispatchTask {
	alarmId := ""
	if m.AlarmId != nil {
		alarmId = *m.AlarmId
	}
	return types.DispatchTask{
		Id:            m.Id,
		TaskNo:        m.TaskNo,
		Title:         m.Title,
		Source:        int32(m.Source),
		AlarmId:       alarmId,
		ZoneCode:      m.ZoneCode,
		RequiredSkill: m.RequiredSkill,
		Priority:      int32(m.Priority),
		Status:        int32(m.Status),
		AssigneeId:    m.AssigneeId,
		AssigneeName:  m.AssigneeName,
		Description:   m.Description,
		CreatedAt:     m.CreatedAt.Unix(),
		UpdatedAt:     m.UpdatedAt.Unix(),
	}
}

// toStaffDTO 将人员模型转换为对外契约对象.
func toStaffDTO(m *model.DispatchStaff) types.StaffItem {
	return types.StaffItem{
		StaffId:   m.StaffId,
		Name:      m.Name,
		Phone:     m.Phone,
		ZoneCode:  m.ZoneCode,
		Skills:    m.Skills,
		OnDuty:    int32(m.OnDuty),
		Status:    int32(m.Status),
		UpdatedAt: m.UpdatedAt.Unix(),
	}
}

// validPriority 校验优先级取值.
func validPriority(p int32) bool {
	switch int8(p) {
	case model.PriorityUrgent, model.PriorityHigh, model.PriorityNormal:
		return true
	}
	return false
}

// clampTaskPage 分页参数兜底: page 从 1 起, pageSize 限制在 1~maxPageSize.
func clampTaskPage(page, pageSize int64) (int64, int64) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}
