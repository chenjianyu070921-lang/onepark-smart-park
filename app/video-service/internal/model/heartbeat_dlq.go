package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// HeartbeatDLQ 心跳消费死信台账(video_db.video_dlq).
// 记录心跳链路无法处理的消息, 便于排查"在线状态为何停更"; 不参与在线判定主流程.
type HeartbeatDLQ struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Topic       string    `gorm:"column:topic;type:varchar(64);not null;default:''" json:"topic"`
	PartitionNo int       `gorm:"column:partition_no;not null;default:0" json:"partition_no"`
	MsgOffset   int64     `gorm:"column:msg_offset;not null;default:0" json:"msg_offset"`
	DeviceID    string    `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	Payload     string    `gorm:"column:payload;type:text" json:"payload"`
	ErrorMsg    string    `gorm:"column:error_msg;type:varchar(512);not null;default:''" json:"error_msg"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

// TableName 指定心跳死信台账表名.
func (HeartbeatDLQ) TableName() string { return "video_dlq" }

// HeartbeatDLQModel 心跳死信台账数据访问层, 接口化以便单测替换.
type HeartbeatDLQModel interface {
	// Create 写入一条心跳死信记录.
	Create(ctx context.Context, d *HeartbeatDLQ) error
}

type heartbeatDLQModel struct {
	db *gorm.DB
}

// NewHeartbeatDLQModel 构造基于 GORM 的心跳死信台账实现.
func NewHeartbeatDLQModel(db *gorm.DB) HeartbeatDLQModel {
	return &heartbeatDLQModel{db: db}
}

func (m *heartbeatDLQModel) Create(ctx context.Context, d *HeartbeatDLQ) error {
	return m.db.WithContext(ctx).Create(d).Error
}
