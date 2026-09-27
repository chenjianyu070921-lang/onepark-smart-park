package consumer

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/common/gormx"
)

// AlarmHandler 把告警事件落成调度工单。
type AlarmHandler struct {
	logx.Logger
	db *gormx.DB
	// defaultTenantID 告警消息体**不携带**租户信息, 自动建单只能落到这个兜底园区。
	// 与 M2 workorder-service / M4 billing-service 同一约定(均取 config.DefaultTenantId)。
	defaultTenantID int64
}

// NewAlarmHandler 构造告警建单处理器。
func NewAlarmHandler(db *gormx.DB, defaultTenantID int64) *AlarmHandler {
	return &AlarmHandler{
		Logger:          logx.WithContext(context.Background()),
		db:              db,
		defaultTenantID: defaultTenantID,
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
		TaskNo: model.NewTaskNo(),
		// 必须写租户: 列表/详情/状态流转都按 ctx 的租户过滤, 不写就恒为 0 ——
		// 网关注入非 0 租户时, 自动建的工单会**建出来就查不到**。
		// 告警消息没有租户来源, 只能落配置里的兜底园区。
		TenantID:      h.defaultTenantID,
		Title:         draft.Title,
		Source:        model.SourceAlarm,
		AlarmId:       &draft.AlarmID,
		ZoneCode:      draft.ZoneCode,
		RequiredSkill: draft.RequiredSkill,
		Priority:      draft.Priority,
		Status:        model.StatusPendingAssign,
		Description:   draft.Description,
	}

	// ⚠️ 两种 1062 必须分开处置(2026-09-27 修):
	//
	//	uk_alarm_id 冲突 = 告警重投/多实例 -> 幂等跳过是对的
	//	uk_task_no  冲突 = 单号撞号 -> 若也当成"已建单"跳过, 真实告警就**建不出工单而无人察觉**
	//	                (消息位移已提交, 不会重投)。所以这里必须换号重试。
	//
	// 这个 Bug 是演示 seed 脚本连建 5 张单挂了 1 张才暴露出来的 —— 单号旧实现同秒只有
	// 1000 种取值, 告警爆发时必然踩到。
	const maxTaskNoAttempts = 3
	var createErr error
	for attempt := 1; attempt <= maxTaskNoAttempts; attempt++ {
		task.Id = 0 // 上一轮失败不留残值
		createErr = h.db.WithContext(ctx).Create(task).Error
		if createErr == nil {
			break
		}
		if model.IsDuplicateKeyOn(createErr, "uk_alarm_id") {
			// 重复告警是正常现象(重投/多实例), 按幂等处理而不是报错。
			h.Infof("[consumer] 告警已建单, 幂等跳过: alarmId=%s", draft.AlarmID)
			return nil
		}
		if !model.IsDuplicateKeyOn(createErr, "uk_task_no") {
			break
		}
		h.Errorf("[consumer] 单号撞号, 换号重试(第 %d/%d 次): %s", attempt, maxTaskNoAttempts, task.TaskNo)
		task.TaskNo = model.NewTaskNo()
	}
	if createErr != nil {
		return fmt.Errorf("创建调度工单失败: %w", createErr)
	}

	if err := h.db.WithContext(ctx).Create(&model.DispatchTaskLog{
		TenantID:   task.TenantID, // 审计也写租户(与主表同源)
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


