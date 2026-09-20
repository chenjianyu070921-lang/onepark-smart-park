package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// 设备状态枚举, 与 device.status 列一致.
const (
	DeviceStatusOffline int8 = 0 // 离线/未激活
	DeviceStatusOnline  int8 = 1 // 在线
	DeviceStatusFault   int8 = 2 // 故障
)

type (
	DeviceModel interface {
		Insert(ctx context.Context, d *Device) error
		FindByDeviceID(ctx context.Context, deviceID string) (*Device, error)
		FindByProductKeyAndName(ctx context.Context, productKey, deviceName string) (*Device, error)
		FindList(ctx context.Context, page, size int, productKey string, status int8) ([]*Device, int64, error)
		// CountGroupByStatus 按状态分组统计未删除设备数; productKey 为空表示全部产品,
		// deviceType 为 0 表示全部设备类型(1地磁 2门禁 3摄像头...).
		// 返回 map[status]count, 仅包含库中实际出现的状态取值, 由调用方做枚举映射与求和.
		CountGroupByStatus(ctx context.Context, productKey string, deviceType int8) (map[int8]int64, error)
		UpdateStatus(ctx context.Context, deviceID string, status int8) error
		// UpdateOnline 更新在线状态并刷新最后在线时间, 由遥测消费端驱动.
		UpdateOnline(ctx context.Context, deviceID string, status int8, at time.Time) error
		SoftDelete(ctx context.Context, deviceID string) error
	}

	deviceModel struct {
		db *gorm.DB
	}
)

func NewDeviceModel(db *gorm.DB) DeviceModel {
	return &deviceModel{db: db}
}

func (m *deviceModel) Insert(ctx context.Context, d *Device) error {
	return m.db.WithContext(ctx).Create(d).Error
}

func (m *deviceModel) FindByDeviceID(ctx context.Context, deviceID string) (*Device, error) {
	var d Device
	if err := m.db.WithContext(ctx).Where("device_id = ? AND deleted_at IS NULL", deviceID).First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (m *deviceModel) FindByProductKeyAndName(ctx context.Context, productKey, deviceName string) (*Device, error) {
	var d Device
	if err := m.db.WithContext(ctx).
		Where("product_key = ? AND device_name = ? AND deleted_at IS NULL", productKey, deviceName).
		First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (m *deviceModel) FindList(ctx context.Context, page, size int, productKey string, status int8) ([]*Device, int64, error) {
	var list []*Device
	var total int64
	tx := m.db.WithContext(ctx).Model(&Device{}).Where("deleted_at IS NULL")
	if productKey != "" {
		tx = tx.Where("product_key = ?", productKey)
	}
	if status != -1 {
		tx = tx.Where("status = ?", status)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Offset((page - 1) * size).Limit(size).Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// statusCount 是 COUNT ... GROUP BY status 的扫描行.
type statusCount struct {
	Status int8  `gorm:"column:status"`
	Cnt    int64 `gorm:"column:cnt"`
}

func (m *deviceModel) CountGroupByStatus(ctx context.Context, productKey string, deviceType int8) (map[int8]int64, error) {
	var rows []statusCount
	tx := m.db.WithContext(ctx).Model(&Device{}).
		Select("status, COUNT(*) AS cnt").
		Where("deleted_at IS NULL")
	if productKey != "" {
		tx = tx.Where("product_key = ?", productKey)
	}
	if deviceType != 0 {
		tx = tx.Where("type = ?", deviceType)
	}
	if err := tx.Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[int8]int64, len(rows))
	for _, r := range rows {
		counts[r.Status] = r.Cnt
	}
	return counts, nil
}

func (m *deviceModel) UpdateStatus(ctx context.Context, deviceID string, status int8) error {
	return m.db.WithContext(ctx).Model(&Device{}).
		Where("device_id = ? AND deleted_at IS NULL", deviceID).
		Update("status", status).Error
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

func (m *deviceModel) SoftDelete(ctx context.Context, deviceID string) error {
	return m.db.WithContext(ctx).Model(&Device{}).
		Where("device_id = ? AND deleted_at IS NULL", deviceID).
		Update("deleted_at", gorm.Expr("NOW()")).Error
}
