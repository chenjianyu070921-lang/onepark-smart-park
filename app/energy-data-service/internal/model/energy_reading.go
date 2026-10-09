package model

import (
	"context"
	"sort"
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
}BB

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
// 必须用差分法: 增量 = 相邻两条读数之差, 记到后一条读数所在的桶。
// 不能按桶内 MAX-MIN 算 —— 稀疏上报的设备(一天只报几次)一个桶里往往只有一条读数,
// 桶内 MAX-MIN 恒为 0, 跨桶的增量也全被丢掉, 导致各桶之和 <> 区间总量。
// 读数回退(换表/重置)时增量按 0 记, 不产生负用量。
// layout 是 Go 的时间格式: "2006-01-02 15:00" 按小时, "2006-01-02" 按天
func (m *EnergyReadingModel) ListUsage(ctx context.Context, deviceID string, start, end time.Time, layout string) ([]UsagePoint, error) {
	var rows []EnergyReading
	err := m.db.WithContext(ctx).
		Model(&EnergyReading{}).
		Select("reported_at", "energy_kwh").
		Where("device_id = ? AND reported_at >= ? AND reported_at < ?", deviceID, start, end).
		Order("reported_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	acc := make(map[string]float64)
	for i := 1; i < len(rows); i++ {
		delta := rows[i].EnergyKwh - rows[i-1].EnergyKwh
		if delta < 0 {
			delta = 0
		}
		acc[rows[i].ReportedAt.Format(layout)] += delta
	}

	points := make([]UsagePoint, 0, len(acc))
	for b, u := range acc {
		points = append(points, UsagePoint{Bucket: b, Usage: u})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Bucket < points[j].Bucket })
	return points, nil
}
