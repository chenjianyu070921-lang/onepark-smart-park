package consumer

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/common/gormx"
)

// AlarmHandler 把告警事件落成调度工单。
type AlarmHandler struct {
	logx.Logger
	db *gormx.DB
}

// NewAlarmHandler 构造告警建单处理器。
func NewAlarmHandler(db *gormx.DB) *AlarmHandler {
	return &AlarmHandler{
		Logger: logx.WithContext(context.Background()),
		db:     db,
	}
}

// Handle 解析一条告警消息并幂等建单。
//
// 幂等由 dispatch_task 的 uk_alarm_id 唯一索引保证(而非依赖 Redis 去重):
// 即使同一告警被重复投递、或多实例并发消费, 也只会落出**一张**工单。
// alarm_id 列可空是关键 —— MySQL 唯一索引允许多个 NULL, 因此多张人工单不会互相冲突。
//
// 返回 nil 表示"本条已处理完(含幂等跳过与坏消息丢弃)", 消费循环据此提交位移。
func (h *AlarmHandler) Handle(ctx context.Context, value []byte) error {
	evt, err := DecodeAlarm(value)
	if err != nil {
		// 坏消息必须"记录后跳过": 若返回 error, 位移不提交, 整个分区会被这一条卡死。
		h.Errorf("[consumer] 丢弃非法告警消息: %v", err)
		return nil
	}
	if h.db == nil {
		return fmt.Errorf("数据库未初始化, 无法自动建单")
	}

	draft := BuildTaskDraft(evt)
	task := &model.DispatchTask{
		TaskNo:      model.NewTaskNo(),
		Title:       draft.Title,
		Source:      model.SourceAlarm,
		AlarmId:     &draft.AlarmID,
		ZoneCode:    draft.ZoneCode,
		Priority:    draft.Priority,
		Status:      model.StatusPendingAssign,
		Description: draft.Description,
	}

	if err := h.db.WithContext(ctx).Create(task).Error; err != nil {
		if isDuplicateEntry(err) {
			// 重复告警是正常现象(重投/多实例), 按幂等处理而不是报错。
			h.Infof("[consumer] 告警已建单, 幂等跳过: alarmId=%s", draft.AlarmID)
			return nil
		}
		return fmt.Errorf("创建调度工单失败: %w", err)
	}

	if err := h.db.WithContext(ctx).Create(&model.DispatchTaskLog{
		TaskId:     task.Id,
		FromStatus: 0,
		ToStatus:   model.StatusPendingAssign,
		Action:     state.ActionCreate,
		Remark:     fmt.Sprintf("告警自动建单: %s", draft.AlarmID),
	}).Error; err != nil {
		// 审计失败不影响主流程, 但必须留痕。
		h.Errorf("[consumer] 写审计流水失败: taskId=%d, err=%v", task.Id, err)
	}

	h.Infof("[consumer] 告警自动建单成功: taskNo=%s, alarmId=%s, zone=%q, priority=%d",
		task.TaskNo, draft.AlarmID, draft.ZoneCode, draft.Priority)
	return nil
}

// isDuplicateEntry 判断是否唯一键冲突(MySQL 1062)。
// 用错误信息匹配而非引入 mysql 驱动包, 避免为一次判断新增直接依赖。
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
