package model

import (
	"time"

	"gorm.io/datatypes"
)

// 指令状态, 与 command_log.status 列保持一致.
const (
	CommandStatusPending int8 = 0 // 待发送
	CommandStatusSent    int8 = 1 // 已下发
	CommandStatusSuccess int8 = 2 // 执行成功
	CommandStatusFailed  int8 = 3 // 执行失败
	CommandStatusTimeout int8 = 4 // 超时
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
	RetryCount  int            `gorm:"column:retry_count;type:int;not null;default:0" json:"retry_count"`
	SentAt      *time.Time     `gorm:"column:sent_at" json:"sent_at"`
	ExecutedAt  *time.Time     `gorm:"column:executed_at" json:"executed_at"`
	TimeoutAt   time.Time      `gorm:"column:timeout_at;not null;index:idx_timeout,priority:2" json:"timeout_at"`
	CreatedAt   time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
}

func (CommandLog) TableName() string {
	return "command_log"
}
