// Package model 定义 dispatch-service 的 GORM 数据模型(dispatch_db).
package model

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
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
	// TenantID 园区ID, RBAC 行级隔离维度(列由 l2_tenant_id_migration.sql 迁移新增).
	TenantID       int64      `gorm:"column:tenant_id;index:idx_tenant"`
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
	// TenantID 园区ID, 与主表 dispatch_task.tenant_id 保持一致.
	TenantID   int64     `gorm:"column:tenant_id;index:idx_tenant"`
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

// taskNoSeq 单号序号计数器(进程内).
// 随机起点: 多实例部署时让各实例的序列错开, 降低跨实例撞号概率.
var taskNoSeq uint32

func init() { taskNoSeq = uint32(rand.Intn(1000)) }

// NewTaskNo 生成调度单号: DT + yyyyMMddHHmmss + 3 位序号.
//
// 序号用**进程内原子计数器**, 不再用随机数 —— 这是 2026-09-27 修的一个真 Bug:
// 旧实现 `rand.Intn(1000)` 在同秒内只有 1000 种取值, 建 5 张单约有 1% 概率撞号、
// 10 张约 4.4%、50 张约 71%(演示 seed 脚本 5 张单里挂了 1 张, 才暴露出来)。
// 而撞 `uk_task_no` 的后果**不只是报错**: 告警自动建单路径把 1062 一概当成
// 「告警已建单」静默跳过 —— 真实告警会因此**建不出工单而无人察觉**。
//
// 计数器保证同进程内每秒 ≤1000 张单**绝不重复**; 超过 1000 张/秒(或跨实例并发)仍可能撞,
// 由两条建单路径的换号重试兜底(见 IsDuplicateKeyOn 的用法)。
//
// 放在 model 层, 是因为「人工建单」与「告警自动建单」两条路径都要用它.
func NewTaskNo() string {
	n := atomic.AddUint32(&taskNoSeq, 1)
	return fmt.Sprintf("DT%s%03d", time.Now().Format("20060102150405"), n%1000)
}

// IsDuplicateKeyOn 判断错误是否为**指定唯一索引**的冲突(MySQL 1062).
//
// 为什么必须带索引名: 同一个 1062 对不同唯一键的处置完全相反 ——
//
//	uk_alarm_id 冲突 = 告警重投, 幂等跳过 **正确**;
//	uk_task_no  冲突 = 单号撞号, 跳过就等于**把没建成的单当成早就建过**, 真实告警被丢掉。
//
// 一律按"已存在"处理正是上面那个静默丢单 Bug 的成因, 所以这里刻意要求指名索引。
func IsDuplicateKeyOn(err error, index string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, "1062") && !strings.Contains(msg, "Duplicate entry") {
		return false
	}
	return strings.Contains(msg, index)
}
