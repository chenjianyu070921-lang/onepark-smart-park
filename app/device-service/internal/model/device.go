package model

import (
	"time"

	"gorm.io/gorm"

	"onepark/common/shadow"
)

type Device struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID     string `gorm:"column:device_id;type:char(36);uniqueIndex:uk_device_id;not null" json:"device_id"`
	DeviceName   string `gorm:"column:device_name;type:varchar(64);not null;default:''" json:"device_name"`
	DeviceSecret string `gorm:"column:device_secret;type:varchar(255);not null;default:''" json:"-"`
	ProductKey   string `gorm:"column:product_key;type:varchar(32);not null;index:idx_product_status,priority:1" json:"product_key"`
	// TenantID 租户 ID(多园区隔离), 0 平台默认; 生产端充入 Kafka 消息供下游租户过滤.
	TenantID int64 `gorm:"column:tenant_id;type:bigint;not null;default:0;index:idx_tenant" json:"tenant_id"`
	// ZoneID 能源区域编码(M4 计费/分析维度), 空表示未分区.
	ZoneID string `gorm:"column:zone_id;type:varchar(64);not null;default:'';index:idx_zone" json:"zone_id"`
	// Type 设备类型: 1地磁 2门禁 3摄像头..., 0 未分类; 与 gRPC GetDeviceResp.type 同口径
	Type       int8   `gorm:"column:type;type:tinyint;not null;default:0;index:idx_type" json:"type"`
	ParkID     string `gorm:"column:park_id;type:varchar(32);not null;default:'';index:idx_park" json:"park_id"`
	BuildingID string `gorm:"column:building_id;type:varchar(32);not null;default:''" json:"building_id"`
	Floor      string `gorm:"column:floor;type:varchar(16);not null;default:''" json:"floor"`
	Location   string `gorm:"column:location;type:varchar(128);not null;default:''" json:"location"`
	// Latitude/Longitude 设备坐标(WGS84), 未建档定位时为 NULL
	Latitude     *float64       `gorm:"column:latitude;type:decimal(10,7)" json:"latitude,omitempty"`
	Longitude    *float64       `gorm:"column:longitude;type:decimal(10,7)" json:"longitude,omitempty"`
	Status       int8           `gorm:"column:status;type:tinyint;not null;default:0;index:idx_product_status,priority:2" json:"status"`
	LastOnlineAt *time.Time     `gorm:"column:last_online_at" json:"last_online_at"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt    time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"column:deleted_at;index:idx_deleted_at" json:"deleted_at"`
}

func (Device) TableName() string {
	return "device"
}

// Shadow 设备影子共享模型(与 shadow-service 共用 common/shadow 权威定义).
type Shadow = shadow.Shadow
