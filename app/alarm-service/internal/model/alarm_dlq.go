package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// 死信台账状态(docs/m3/06 §5.3).
const (
	DLQStatusPending  int8 = 0 // 待处理
	DLQStatusReplayed int8 = 1 // 已重放
	DLQStatusDropped  int8 = 2 // 已丢弃(人工判定无效)
)

// AlarmDLQ 消费死信台账(alarm_db.alarm_dlq).
// 记录最终处理失败的消息, 便于排查与人工重放; 不参与告警主流程.
type AlarmDLQ struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID    int64     `gorm:"column:tenant_id;not null;default:0" json:"tenant_id"`
	Topic       string    `gorm:"column:topic;type:varchar(64);not null;default:''" json:"topic"`
	PartitionNo int       `gorm:"column:partition_no;not null;default:0" json:"partition_no"`
	MsgOffset   int64     `gorm:"column:msg_offset;not null;default:0" json:"msg_offset"`
	RequestID   string    `gorm:"column:request_id;type:varchar(64);not null;default:''" json:"request_id"`
	DeviceID    string    `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	EventType   string    `gorm:"column:event_type;type:varchar(64);not null;default:''" json:"event_type"`
	Payload     string    `gorm:"column:payload;type:text" json:"payload"`
	ErrorMsg    string    `gorm:"column:error_msg;type:varchar(512);not null;default:''" json:"error_msg"`
	RetryCount  int       `gorm:"column:retry_count;not null;default:0" json:"retry_count"`
	Status      int8      `gorm:"column:status;not null;default:0" json:"status"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// TableName 指定死信台账表名.
func (AlarmDLQ) TableName() string { return "alarm_dlq" }

// DeadLetterModel 死信台账数据访问层, 接口化以便单测替换.
type DeadLetterModel interface {
	// Create 写入一条死信记录.
	Create(ctx context.Context, d *AlarmDLQ) error
}

type deadLetterModel struct {
	db *gorm.DB
}

// NewDeadLetterModel 构造基于 GORM 的死信台账实现.
func NewDeadLetterModel(db *gorm.DB) DeadLetterModel {
	return &deadLetterModel{db: db}
}

func (m *deadLetterModel) Create(ctx context.Context, d *AlarmDLQ) error {
	return m.db.WithContext(ctx).Create(d).Error
}
