package model

import (
	"context"
	"sort"
	"time"

	"gorm.io/gorm"

	"onepark/app/billing-service/internal/calc"
)

// EnergyReading 对应表 energy_reading
// 注意: 本服务只读这张表, 写入由 energy-data-service 的 Kafka 消费者负责
type EnergyReading struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	DeviceID   string    `gorm:"column:device_id;type:varchar(64);not null"`
	ZoneID     string    `gorm:"column:zone_id;type:varchar(64);not null"`
	EnergyKwh  float64   `gorm:"column:energy_kwh;type:double;not null"`
	ReportedAt time.Time `gorm:"column:reported_at;type:datetime;not null"`
}

func (EnergyReading) TableName() string { return "energy_reading" }

type EnergyReadingModel struct {
	db *gorm.DB
}

func NewEnergyReadingModel(db *gorm.DB) *EnergyReadingModel {
	return &EnergyReadingModel{db: db}
}

// TotalUsage 算某个区域在 [start, end) 的总用量(度)
// 电表读数是累计值, 必须先按设备各算各的差值再求和, 不能拿全表 MAX-MIN(那样会混入设备间的基数差)
func (m *EnergyReadingModel) TotalUsage(ctx context.Context, zoneID string, start, end time.Time) (float64, error) {
	q := "SELECT COALESCE(SUM(t.usage_kwh), 0) FROM (" +
		"SELECT MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh FROM energy_reading " +
		"WHERE zone_id = ? AND reported_at >= ? AND reported_at < ? GROUP BY device_id) AS t"

	var total float64
	if err := m.db.WithContext(ctx).Raw(q, zoneID, start, end).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// ListHourlyUsage 按"钟点"汇总用量, 峰谷计价要用
//
// 返回的是整个账期内每个钟点的累计用量: 比如查 9 月份, Hour=8 表示
// "30 天里每天早上 8 点这一个小时, 一共用了多少度", 这样才能按峰谷时段分别计价。
//
// ⚠️ 这里不能用 "每个小时桶内 MAX-MIN" 再相加 —— 那样会把跨小时的增量漏掉:
// 假设 9 点读到 12007.5、10 点读到 12010, 两个桶各自只有一条数据时差值都是 0,
// 结果 2.5 度就凭空消失了, 账单会少算钱。
//
// 正确做法(差分法): 取出每台设备每个(天,小时)桶的最后读数, 按时间排序后
// 用 "本桶读数 - 上一桶读数" 算出这一小时用了多少; 第一个桶用区间内的第一条读数做基准。
// 这样所有小时的用量加起来, 正好等于总用量(最后读数 - 第一条读数), 一分不差。
func (m *EnergyReadingModel) ListHourlyUsage(ctx context.Context, zoneID string, start, end time.Time) ([]calc.HourUsage, error) {
	// 1. 每台设备在每个(天,小时)桶里的最后一条读数
	bucketSQL := "SELECT e.device_id, HOUR(e.reported_at) AS hour, e.energy_kwh " +
		"FROM energy_reading e " +
		"WHERE e.zone_id = ? AND e.reported_at >= ? AND e.reported_at < ? " +
		"AND e.reported_at = (SELECT MAX(r.reported_at) FROM energy_reading r " +
		"WHERE r.device_id = e.device_id AND r.zone_id = ? " +
		"AND r.reported_at >= ? AND r.reported_at < ? " +
		"AND DATE_FORMAT(r.reported_at, '%Y-%m-%d %H') = DATE_FORMAT(e.reported_at, '%Y-%m-%d %H')) " +
		"ORDER BY e.device_id, e.reported_at ASC"

	buckets := make([]struct {
		DeviceID  string  `gorm:"column:device_id"`
		Hour      int     `gorm:"column:hour"`
		EnergyKwh float64 `gorm:"column:energy_kwh"`
	}, 0)
	if err := m.db.WithContext(ctx).Raw(bucketSQL, zoneID, start, end, zoneID, start, end).Scan(&buckets).Error; err != nil {
		return nil, err
	}
	if len(buckets) == 0 {
		return nil, nil
	}

	// 2. 每台设备在账期内的第一条读数, 作为差分的起点
	firstSQL := "SELECT e.device_id, e.energy_kwh FROM energy_reading e " +
		"WHERE e.zone_id = ? AND e.reported_at >= ? AND e.reported_at < ? " +
		"AND e.reported_at = (SELECT MIN(r.reported_at) FROM energy_reading r " +
		"WHERE r.device_id = e.device_id AND r.zone_id = ? AND r.reported_at >= ? AND r.reported_at < ?)"
	firsts := make([]struct {
		DeviceID  string  `gorm:"column:device_id"`
		EnergyKwh float64 `gorm:"column:energy_kwh"`
	}, 0)
	if err := m.db.WithContext(ctx).Raw(firstSQL, zoneID, start, end, zoneID, start, end).Scan(&firsts).Error; err != nil {
		return nil, err
	}
	firstOf := make(map[string]float64, len(firsts))
	for _, f := range firsts {
		firstOf[f.DeviceID] = f.EnergyKwh
	}

	// 3. 逐台设备做差分, 把每个小时的用量累加到对应的钟点上
	hourUsage := make(map[int]float64)
	prev := make(map[string]float64)
	for _, b := range buckets {
		base, ok := prev[b.DeviceID]
		if !ok {
			base, ok = firstOf[b.DeviceID]
			if !ok {
				continue
			}
		}
		delta := b.EnergyKwh - base
		if delta > 0 { // 读数是累计值, 正常情况下只增不减; 换表或重置导致的负值直接跳过
			hourUsage[b.Hour] += delta
		}
		prev[b.DeviceID] = b.EnergyKwh
	}

	list := make([]calc.HourUsage, 0, len(hourUsage))
	for h, u := range hourUsage {
		list = append(list, calc.HourUsage{Hour: h, Usage: u})
	}
	// 按钟点排序, 输出的顺序稳定一些
	sort.Slice(list, func(i, j int) bool { return list[i].Hour < list[j].Hour })
	return list, nil
}
