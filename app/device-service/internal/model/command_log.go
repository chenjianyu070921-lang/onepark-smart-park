package model

import (
	"time"

	"gorm.io/datatypes"
)

type CommandLog struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID   string         `gorm:"column:request_id;type:char(36);uniqueIndex:uk_request_id;not null" json:"request_id"`
	DeviceID    string         `gorm:"column:device_id;type:char(36);not null;index:idx_device_status,priority:1" json:"device_id"`
	CommandType string         `gorm:"column:command_type;type:varchar(32);not null;default:''" json:"command_type"`
	Payload     datatypes.JSON `gorm:"column:payload;type:json;not null" json:"payload"`
	Mode        int8           `gorm:"column:mode;type:tinyint;not null;default:1" json:"mode"`
	Status      int8           `gorm:"column:status;type:tinyint;not null;default:0;index:idx_device_status,priority:2;index:idx_timeout,priority:1" json:"status"`
	Response    datatypes.JSON `gorm:"column:response;type:json" json:"response"`
	SentAt      *time.Time     `gorm:"column:sent_at" json:"sent_at"`
	ExecutedAt  *time.Time     `gorm:"column:executed_at" json:"executed_at"`
	TimeoutAt   time.Time      `gorm:"column:timeout_at;not null;index:idx_timeout,priority:2" json:"timeout_at"`
	CreatedAt   time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
}

func (CommandLog) TableName() string {
	return "command_log"
}
