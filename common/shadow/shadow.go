// Package shadow 设备影子的共享 GORM 模型与版本语义.
// 此前 device-service 与 shadow-service 各持一份结构体(且版本语义分叉:
// 一侧无条件覆盖、一侧乐观锁), 本包将其合一, 两服务均以类型别名引用.
package shadow

import (
	"time"

	"gorm.io/datatypes"
)

// Shadow 设备影子, 对应 shadow 表(desired/reported JSON + version 乐观锁).
type Shadow struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID  string         `gorm:"column:device_id;type:char(36);uniqueIndex:uk_device_id;not null" json:"device_id"`
	Desired   datatypes.JSON `gorm:"column:desired;type:json" json:"desired"`
	Reported  datatypes.JSON `gorm:"column:reported;type:json" json:"reported"`
	Version   uint           `gorm:"column:version;type:int unsigned;not null;default:0" json:"version"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

// TableName 表名.
func (Shadow) TableName() string { return "shadow" }
