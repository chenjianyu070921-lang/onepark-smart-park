package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Device struct {
	ID           uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID     string         `gorm:"column:device_id;type:char(36);uniqueIndex:uk_device_id;not null" json:"device_id"`
	DeviceName   string         `gorm:"column:device_name;type:varchar(64);not null;default:''" json:"device_name"`
	DeviceSecret string         `gorm:"column:device_secret;type:varchar(255);not null;default:''" json:"-"`
	ProductKey   string         `gorm:"column:product_key;type:varchar(32);not null;index:idx_product_status,priority:1" json:"product_key"`
	ParkID       string         `gorm:"column:park_id;type:varchar(32);not null;default:'';index:idx_park" json:"park_id"`
	BuildingID   string         `gorm:"column:building_id;type:varchar(32);not null;default:''" json:"building_id"`
	Floor        string         `gorm:"column:floor;type:varchar(16);not null;default:''" json:"floor"`
	Location     string         `gorm:"column:location;type:varchar(128);not null;default:''" json:"location"`
	Status       int8           `gorm:"column:status;type:tinyint;not null;default:0;index:idx_product_status,priority:2" json:"status"`
	LastOnlineAt *time.Time     `gorm:"column:last_online_at" json:"last_online_at"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt    time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"column:deleted_at;index:idx_deleted_at" json:"deleted_at"`
}

func (Device) TableName() string {
	return "device"
}

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
