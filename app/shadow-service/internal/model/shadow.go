package model

import (
	"time"

	"gorm.io/datatypes"
)

// Shadow 设备影子, 与 device_db.shadow 表对应.
// 注意: 该结构与 device-service 的 model.Shadow 保持一致, 后续应下沉到 common 共享.
type Shadow struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID  string         `gorm:"column:device_id;type:char(36);uniqueIndex:uk_device_id;not null" json:"device_id"`
	Desired   datatypes.JSON `gorm:"column:desired;type:json" json:"desired"`
	Reported  datatypes.JSON `gorm:"column:reported;type:json" json:"reported"`
	Version   uint           `gorm:"column:version;type:int unsigned;not null;default:0" json:"version"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Shadow) TableName() string {
	return "shadow"
}
