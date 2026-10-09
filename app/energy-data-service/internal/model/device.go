package model

import (
	"context"

	"gorm.io/gorm"
)

// Device 对应 M1 的 device 表, M4 只读不写
// 只关心 device_id 和 building_id(楼栋), 后者用来当能耗数据的区域归属
type Device struct {
	DeviceID   string `gorm:"column:device_id"`
	BuildingID string `gorm:"column:building_id"`
}

func (Device) TableName() string {
	return "device"
}

type DeviceModel struct {
	db *gorm.DB
}

func NewDeviceModel(db *gorm.DB) *DeviceModel {
	return &DeviceModel{db: db}
}

// FindZone 查设备属于哪个区域
// M1 的遥测消息里只有 device_id, 没有区域; 而日报/月报/账单全是按区域聚合的,
// 所以这里拿 device.building_id 当区域补上。
// 查不到返回空串(调用方决定是兜底还是丢弃), 不算错误。
func (m *DeviceModel) FindZone(ctx context.Context, deviceID string) (string, error) {
	// 用 Find 而不是 Take: 设备没建档属于正常情况, 别让它打一条 "record not found" 刷日志
	var list []Device
	err := m.db.WithContext(ctx).
		Select("device_id", "building_id").
		Where("device_id = ?", deviceID).
		Limit(1).
		Find(&list).Error
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", nil // 设备没建档, 由调用方兜底
	}
	return list[0].BuildingID, nil
}
