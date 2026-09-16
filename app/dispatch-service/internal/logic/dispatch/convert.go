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
		Id:           m.Id,
		TaskNo:       m.TaskNo,
		Title:        m.Title,
		Source:       int32(m.Source),
		AlarmId:      alarmId,
		ZoneCode:     m.ZoneCode,
		Priority:     int32(m.Priority),
		Status:       int32(m.Status),
		AssigneeId:   m.AssigneeId,
		AssigneeName: m.AssigneeName,
		Description:  m.Description,
		CreatedAt:    m.CreatedAt.Unix(),
		UpdatedAt:    m.UpdatedAt.Unix(),
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
