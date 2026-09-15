package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// EnergyReading 对应数据库表 energy_reading, 设备每上报一次读数就加一行
type EnergyReading struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	DeviceID   string    `gorm:"column:device_id;type:varchar(64);not null" json:"deviceId"`
	ZoneID     string    `gorm:"column:zone_id;type:varchar(64);not null" json:"zoneId"`
	EnergyKwh  float64   `gorm:"column:energy_kwh;type:double;not null" json:"energyKwh"`
	PowerKw    *float64  `gorm:"column:power_kw;type:double" json:"powerKw"`
	ReportedAt time.Time `gorm:"column:reported_at;type:datetime;not null" json:"reportedAt"`
	CreatedAt  time.Time `gorm:"column:created_at;type:datetime;autoCreateTime" json:"createdAt"`
}

func (EnergyReading) TableName() string {
	return "energy_reading"
}

type EnergyReadingModel struct {
	db *gorm.DB
}

func NewEnergyReadingModel(db *gorm.DB) *EnergyReadingModel {
	return &EnergyReadingModel{db: db}
}

// Insert 写一条读数进表
func (m *EnergyReadingModel) Insert(ctx context.Context, r *EnergyReading) error {
	return m.db.WithContext(ctx).Create(r).Error
}

// BatchInsert 一次写多条(接口55 消费者攒批后调用)
func (m *EnergyReadingModel) BatchInsert(ctx context.Context, rows []*EnergyReading) error {
	if len(rows) == 0 {
		return nil
	}
	return m.db.WithContext(ctx).CreateInBatches(rows, 100).Error
}

// FindLatest 查某台设备最新的一条读数(接口52实时能耗用)
func (m *EnergyReadingModel) FindLatest(ctx context.Context, deviceID string) (*EnergyReading, error) {
	var r EnergyReading
	err := m.db.WithContext(ctx).
		Where("device_id = ?", deviceID).
		Order("reported_at DESC").
		First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UsageBetween 算某台设备在 [start, end) 这段时间的用量
// 累计读数的用法: 用量 = 期末读数 - 期初读数, 即 MAX - MIN
func (m *EnergyReadingModel) UsageBetween(ctx context.Context, deviceID string, start, end time.Time) (float64, error) {
	var usage float64
	err := m.db.WithContext(ctx).Model(&EnergyReading{}).
		Select("COALESCE(MAX(energy_kwh) - MIN(energy_kwh), 0)").
		Where("device_id = ? AND reported_at >= ? AND reported_at < ?", deviceID, start, end).
		Scan(&usage).Error
	return usage, err
}

// TotalUsageByDevice 算整个园区(或某个区域)在 [start, end) 的总用量
// 注意: 必须先按设备分别算差值再求和, 不能拿全表的 MAX-MIN(那样会漏掉设备间的基数差)
func (m *EnergyReadingModel) TotalUsageByDevice(ctx context.Context, start, end time.Time, zoneID string) (float64, error) {
	var total float64
	q := "SELECT COALESCE(SUM(t.usage_kwh), 0) FROM (" +
		"SELECT MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh FROM energy_reading " +
		"WHERE reported_at >= ? AND reported_at < ?"
	args := []interface{}{start, end}
	if zoneID != "" {
		q += " AND zone_id = ?"
		args = append(args, zoneID)
	}
	q += " GROUP BY device_id) AS t"

	if err := m.db.WithContext(ctx).Raw(q, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// FindLatestAny 查最新的一条读数(可按区域过滤, zoneID 为空表示全园区)
func (m *EnergyReadingModel) FindLatestAny(ctx context.Context, zoneID string) (*EnergyReading, error) {
	var r EnergyReading
	q := m.db.WithContext(ctx)
	if zoneID != "" {
		q = q.Where("zone_id = ?", zoneID)
	}
	err := q.Order("reported_at DESC").First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UsagePoint 一个时间桶的用量, 比如 "2026-09-15 09:00" 这一个小时用了多少度
type UsagePoint struct {
	Bucket string  `gorm:"column:bucket"`
	Usage  float64 `gorm:"column:usage_kwh"` // 不能用 usage, 那是 MySQL 保留字
}

// ListUsage 按时间粒度汇总用量(接口53历史曲线用)
// layout 传 "%Y-%m-%d %H:00" 就是按小时, 传 "%Y-%m-%d" 就是按天
func (m *EnergyReadingModel) ListUsage(ctx context.Context, deviceID string, start, end time.Time, layout string) ([]UsagePoint, error) {
	var points []UsagePoint
	err := m.db.WithContext(ctx).Model(&EnergyReading{}).
		Select("DATE_FORMAT(reported_at, ?) AS bucket, MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh", layout).
		Where("device_id = ? AND reported_at >= ? AND reported_at < ?", deviceID, start, end).
		Group("bucket").
		Order("bucket ASC").
		Scan(&points).Error
	return points, err
}
