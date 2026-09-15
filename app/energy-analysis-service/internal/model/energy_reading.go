package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// EnergyReading 对应数据库表 energy_reading, 设备每上报一次读数就加一行
// 注意: 本服务只读这张表, 写入由 energy-data-service 的 Kafka 消费者负责
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

// TotalUsage 算整个园区(或某个区域)在 [start, end) 的总用量
// 必须先按设备分别算差值再求和: 每台电表的起始读数不同, 直接拿全表 MAX-MIN 会把基数差算进去
func (m *EnergyReadingModel) TotalUsage(ctx context.Context, start, end time.Time, zoneID string) (float64, error) {
	q := "SELECT COALESCE(SUM(t.usage_kwh), 0) FROM (" +
		"SELECT MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh FROM energy_reading " +
		"WHERE reported_at >= ? AND reported_at < ?"
	args := []interface{}{start, end}
	if zoneID != "" {
		q += " AND zone_id = ?"
		args = append(args, zoneID)
	}
	q += " GROUP BY device_id) AS t"

	var total float64
	if err := m.db.WithContext(ctx).Raw(q, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// ZoneUsageRow 一个区域在一段时间内的汇总结果
type ZoneUsageRow struct {
	ZoneID      string  `gorm:"column:zone_id"`
	Usage       float64 `gorm:"column:usage_kwh"`
	DeviceCount int64   `gorm:"column:device_count"`
}

// ListZoneUsage 按区域汇总用量, 用量高的排前面(接口56 日报用)
func (m *EnergyReadingModel) ListZoneUsage(ctx context.Context, start, end time.Time) ([]ZoneUsageRow, error) {
	sub := "SELECT device_id, zone_id, MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh " +
		"FROM energy_reading WHERE reported_at >= ? AND reported_at < ? GROUP BY device_id, zone_id"
	q := "SELECT zone_id, SUM(t.usage_kwh) AS usage_kwh, COUNT(*) AS device_count " +
		"FROM (" + sub + ") AS t GROUP BY zone_id ORDER BY usage_kwh DESC"

	var rows []ZoneUsageRow
	if err := m.db.WithContext(ctx).Raw(q, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DayUsageRow 某一天的用量
type DayUsageRow struct {
	Day   string  `gorm:"column:day"`
	Usage float64 `gorm:"column:usage_kwh"`
}

// ListDailyUsage 按天汇总用量, 日期从早到晚(接口57 月报、接口58 趋势用)
// zoneID 传空表示全园区
//
// 口径说明: 日用量 = 当天最后一条读数 - 当天第一条读数。
// 设备高频上报时(几秒~几分钟一条)这个差值就是当天真实用量;
// 如果某天只上报了一条, 当天算出来就是 0 —— 这是数据不足, 不是算错。
// 同理, 各天之和可能略小于整月的 MAX-MIN, 属于正常现象。
func (m *EnergyReadingModel) ListDailyUsage(ctx context.Context, zoneID string, start, end time.Time) ([]DayUsageRow, error) {
	sub := "SELECT DATE_FORMAT(reported_at, '%Y-%m-%d') AS day, device_id, " +
		"MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh " +
		"FROM energy_reading WHERE reported_at >= ? AND reported_at < ?"
	args := []interface{}{start, end}
	if zoneID != "" {
		sub += " AND zone_id = ?"
		args = append(args, zoneID)
	}
	sub += " GROUP BY day, device_id"

	q := "SELECT day, SUM(t.usage_kwh) AS usage_kwh FROM (" + sub + ") AS t GROUP BY day ORDER BY day ASC"

	var rows []DayUsageRow
	if err := m.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DeviceUsageRow 一台设备的用量
type DeviceUsageRow struct {
	DeviceID string  `gorm:"column:device_id"`
	Usage    float64 `gorm:"column:usage_kwh"`
}

// ListDeviceUsage 按设备汇总某个区域的用量, 用量高的排前面(接口58 区域详情用)
func (m *EnergyReadingModel) ListDeviceUsage(ctx context.Context, zoneID string, start, end time.Time) ([]DeviceUsageRow, error) {
	var rows []DeviceUsageRow
	err := m.db.WithContext(ctx).Model(&EnergyReading{}).
		Select("device_id, MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh").
		Where("zone_id = ? AND reported_at >= ? AND reported_at < ?", zoneID, start, end).
		Group("device_id").
		Order("usage_kwh DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// LastReading 一台设备最后一次的读数
type LastReading struct {
	DeviceID   string    `gorm:"column:device_id"`
	EnergyKwh  float64   `gorm:"column:energy_kwh"`
	ReportedAt time.Time `gorm:"column:reported_at"`
}

// ListLastReading 查每个设备在 [start, end) 内最后一条读数(接口58 用)
// 写法: 外层先限定区域和时间, 再用子查询挑出"该设备在这个区间里最晚的那条"
func (m *EnergyReadingModel) ListLastReading(ctx context.Context, zoneID string, start, end time.Time) ([]LastReading, error) {
	q := "SELECT e.device_id, e.energy_kwh, e.reported_at FROM energy_reading e " +
		"WHERE e.zone_id = ? AND e.reported_at >= ? AND e.reported_at < ? " +
		"AND e.reported_at = (SELECT MAX(reported_at) FROM energy_reading " +
		"WHERE device_id = e.device_id AND zone_id = ? AND reported_at >= ? AND reported_at < ?)"

	var rows []LastReading
	if err := m.db.WithContext(ctx).Raw(q, zoneID, start, end, zoneID, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// CountDevice 数一下这段时间里有多少台设备上报过数据
func (m *EnergyReadingModel) CountDevice(ctx context.Context, start, end time.Time, zoneID string) (int64, error) {
	var n int64
	q := m.db.WithContext(ctx).Model(&EnergyReading{}).
		Where("reported_at >= ? AND reported_at < ?", start, end)
	if zoneID != "" {
		q = q.Where("zone_id = ?", zoneID)
	}
	err := q.Distinct("device_id").Count(&n).Error
	if err != nil {
		return 0, err
	}
	return n, nil
}
