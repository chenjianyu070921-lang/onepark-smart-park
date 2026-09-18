// Package model 定义 dispatch-service 的 GORM 数据模型(dispatch_db).
package model

import (
	"fmt"
	"math/rand"
	"time"
)

// 调度工单状态, 与 dispatch_task.status 取值一致.
const (
	StatusPendingAssign int8 = 1 // 待指派
	StatusAssigned      int8 = 2 // 已指派
	StatusProcessing    int8 = 3 // 处理中
	StatusCompleted     int8 = 4 // 已完成
	StatusClosed        int8 = 5 // 已关闭
)

// 工单来源.
const (
	SourceManual int8 = 1 // 人工创建
	SourceAlarm  int8 = 2 // 告警自动创建
)

// 优先级.
const (
	PriorityUrgent int8 = 1 // 紧急
	PriorityHigh   int8 = 2 // 高
	PriorityNormal int8 = 3 // 普通
)

// AssignExpireWindow 指派超时窗口: 超过该时间未接单, 由 cron 重派(internal/cron/reassign.go)。
//
// 放在 model 层, 是因为「指派时写入超时点」与「cron 判定超时」两处必须用同一个值 ——
// 分开定义一旦不一致, 重派时机就会与超时判定错位。
const AssignExpireWindow = 5 * time.Minute

// MaxReassignDefault 自动重派次数上限的默认值(可由配置覆盖).
//
// 为什么必须有上限: 园区可能"全员不在岗"或技能池为空, 没有上限就会每一轮
// 都重试同几个工单 —— 既刷爆日志, 又掩盖了「真的没人可派」这个事实。
// 达上限后工单被释放回「待指派」, 交由人工介入。
const MaxReassignDefault int64 = 3

// DispatchTask 调度工单.
// AlarmId 用指针: 数据库列可为 NULL, 与唯一索引 uk_alarm_id 配合实现"同一告警只建一张单"——
// MySQL 唯一索引允许多个 NULL, 因此多张人工单(AlarmId 为 NULL)不会互相冲突.
type DispatchTask struct {
	Id             int64      `gorm:"column:id;primaryKey;autoIncrement"`
	TaskNo         string     `gorm:"column:task_no;size:32;uniqueIndex"`
	Title          string     `gorm:"column:title;size:255"`
	Source         int8       `gorm:"column:source"`
	AlarmId        *string    `gorm:"column:alarm_id;size:64"`
	ZoneCode       string     `gorm:"column:zone_code;size:64"`
	RequiredSkill  string     `gorm:"column:required_skill;size:32"` // 所需技能, 空=不限; 自动指派时作为硬优先项
	Priority       int8       `gorm:"column:priority"`
	Status         int8       `gorm:"column:status"`
	AssigneeId     int64      `gorm:"column:assignee_id"`
	AssigneeName   string     `gorm:"column:assignee_name;size:64"`
	Description    string     `gorm:"column:description;size:1024"`
	AssignExpireAt *time.Time `gorm:"column:assign_expire_at"`
	// ReassignCount 已被 cron 自动重派的次数; 达上限后释放回「待指派」。
	ReassignCount int64 `gorm:"column:reassign_count"`
	FinishedAt     *time.Time `gorm:"column:finished_at"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

// TableName 指定表名.
func (DispatchTask) TableName() string { return "dispatch_task" }

// DispatchTaskLog 调度工单状态流转审计.
type DispatchTaskLog struct {
	Id         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	TaskId     int64     `gorm:"column:task_id"`
	FromStatus int8      `gorm:"column:from_status"`
	ToStatus   int8      `gorm:"column:to_status"`
	Action     string    `gorm:"column:action;size:32"`
	Remark     string    `gorm:"column:remark;size:512"`
	OperatorId int64     `gorm:"column:operator_id"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

// TableName 指定表名.
func (DispatchTaskLog) TableName() string { return "dispatch_task_log" }

// NewTaskNo 生成调度单号: DT + yyyyMMddHHmmss + 3 位随机数.
// 随机数用于降低同秒并发碰撞概率; 唯一性最终由 uk_task_no 兜底.
// 放在 model 层, 是因为「人工建单」与「告警自动建单」两条路径都要用它.
func NewTaskNo() string {
	return fmt.Sprintf("DT%s%03d", time.Now().Format("20060102150405"), rand.Intn(1000))
}
