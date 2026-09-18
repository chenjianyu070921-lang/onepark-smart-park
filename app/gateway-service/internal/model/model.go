// Package model 网关侧只读/轻量写模型.
// 网关需要独立校验设备密钥并回写指令回执, 因此直连业务库;
// 只保留接入必需的表与字段, 避免与 device-service 的 internal 包产生耦合.
package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// 设备状态, 与 device-service 保持一致.
const (
	DeviceStatusOffline int8 = 0
	DeviceStatusOnline  int8 = 1
	DeviceStatusFault   int8 = 2
)

// 指令状态, 与 command_log.status 列保持一致.
const (
	CommandStatusSuccess int8 = 2
	CommandStatusFailed  int8 = 3
)

type Device struct {
	ID           uint64         `gorm:"primaryKey;autoIncrement"`
	DeviceID     string         `gorm:"column:device_id"`
	DeviceName   string         `gorm:"column:device_name"`
	DeviceSecret string         `gorm:"column:device_secret"`
	ProductKey   string         `gorm:"column:product_key"`
	TenantID     int64          `gorm:"column:tenant_id"` // 消息充入用, 见 frame.Handler
	ZoneID       string         `gorm:"column:zone_id"`
	Status       int8           `gorm:"column:status"`
	LastOnlineAt *time.Time     `gorm:"column:last_online_at"`
	DeletedAt    gorm.DeletedAt `gorm:"column:deleted_at"`
}

func (Device) TableName() string { return "device" }

type CommandLog struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement"`
	RequestID  string     `gorm:"column:request_id"`
	DeviceID   string     `gorm:"column:device_id"`
	Status     int8       `gorm:"column:status"`
	Response   []byte     `gorm:"column:response;type:json"`
	ExecutedAt *time.Time `gorm:"column:executed_at"`
}

func (CommandLog) TableName() string { return "command_log" }

type (
	DeviceModel interface {
		FindByDeviceID(ctx context.Context, deviceID string) (*Device, error)
		UpdateOnline(ctx context.Context, deviceID string, status int8, at time.Time) error
	}

	deviceModel struct{ db *gorm.DB }

	CommandLogModel interface {
		// Finish 写入指令执行结果; 仅推进待发送/已下发的指令, 已终态不覆盖
		Finish(ctx context.Context, requestID string, status int8, response []byte) error
	}

	commandLogModel struct{ db *gorm.DB }
)

func NewDeviceModel(db *gorm.DB) DeviceModel         { return &deviceModel{db: db} }
func NewCommandLogModel(db *gorm.DB) CommandLogModel { return &commandLogModel{db: db} }

func (m *deviceModel) FindByDeviceID(ctx context.Context, deviceID string) (*Device, error) {
	var d Device
	if err := m.db.WithContext(ctx).
		Where("device_id = ? AND deleted_at IS NULL", deviceID).First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (m *deviceModel) UpdateOnline(ctx context.Context, deviceID string, status int8, at time.Time) error {
	updates := map[string]any{"status": status}
	if status == DeviceStatusOnline {
		updates["last_online_at"] = at
	}
	return m.db.WithContext(ctx).Model(&Device{}).
		Where("device_id = ? AND deleted_at IS NULL", deviceID).
		Updates(updates).Error
}

func (m *commandLogModel) Finish(ctx context.Context, requestID string, status int8, response []byte) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ? AND status IN ?", requestID, []int8{0, 1}).
		Updates(map[string]any{
			"status":      status,
			"response":    response,
			"executed_at": time.Now(),
		}).Error
}
