package consumer

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/common/gormx"
)

// AlarmHandler 把告警事件落成调度工单。
type AlarmHandler struct {
	logx.Logger
	db              *gormx.DB
	defaultTenantId int64 // 告警消息不携带租户, 自动建单落到该园区(RBAC 隔离维度)
}

// NewAlarmHandler 构造告警建单处理器。
func NewAlarmHandler(db *gormx.DB, defaultTenantId int64) *AlarmHandler {
	return &AlarmHandler{
		Logger:          logx.WithContext(context.Background()),
		db:              db,
		defaultTenantId: defaultTenantId,
	}
}

// Handle 解析一条告警消息并幂等建单。
//
// 幂等由 dispatch_task 的 uk_alarm_id 唯一索引保证(而非依赖 Redis 去重):
// 即使同一告警被重复投递、或多实例并发消费, 也只会落出**一张**工单。
// alarm_id 列可空是关键 —— MySQL 唯一索引允许多个 NULL, 因此多张人工单不会互相冲突。
// 主单与审计流水在同一事务写入, 避免出现"有单无流水"的审计断档。
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
		TenantID:    h.defaultTenantId,
		TaskNo:      model.NewTaskNo(),
		Title:       draft.Title,
		Source:      model.SourceAlarm,
		AlarmId:     &draft.AlarmID,
		ZoneCode:    draft.ZoneCode,
		Priority:    draft.Priority,
		Status:      model.StatusPendingAssign,
		Description: draft.Description,
	}

	// createInTx 建单 + 写审计流水(同事务)。task_no 由调用方在重试前重新生成。
	createInTx := func() error {
		return h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(task).Error; err != nil {
				return err
			}
			return tx.Create(&model.DispatchTaskLog{
				TenantID:   h.defaultTenantId,
				TaskId:     task.Id,
				FromStatus: 0,
				ToStatus:   model.StatusPendingAssign,
				Action:     state.ActionCreate,
				Remark:     fmt.Sprintf("告警自动建单: %s", draft.AlarmID),
			}).Error
		})
	}

	if err := createInTx(); err != nil {
		if isDupKey(err, "uk_alarm_id") {
			// 重复告警是正常现象(重投/多实例), 按幂等处理而不是报错。
			h.Infof("[consumer] 告警已建单, 幂等跳过: alarmId=%s", draft.AlarmID)
			return nil
		}
		if isDupKey(err, "uk_task_no") {
			// 单号随机段碰撞: 换单号重试一次, 不能当作幂等跳过(否则告警丢单)。
			h.Errorf("[consumer] task_no 碰撞, 换单号重试: old=%s", task.TaskNo)
			task.TaskNo = model.NewTaskNo()
			if err = createInTx(); err != nil {
				return fmt.Errorf("创建调度工单失败(单号重试后仍失败): %w", err)
			}
		} else {
			return fmt.Errorf("创建调度工单失败: %w", err)
		}
	}

	h.Infof("[consumer] 告警自动建单成功: taskNo=%s, alarmId=%s, zone=%q, priority=%d",
		task.TaskNo, draft.AlarmID, draft.ZoneCode, draft.Priority)
	return nil
}

// isDupKey 判断是否指定唯一键的冲突(MySQL 1062 错误消息中带键名)。
// 区分 uk_alarm_id(幂等命中) 与 uk_task_no(需重试), 避免"单号碰撞被误判为告警已建单"。
func isDupKey(err error, key string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") &&
		strings.Contains(msg, "Duplicate entry") &&
		strings.Contains(msg, key)
}
