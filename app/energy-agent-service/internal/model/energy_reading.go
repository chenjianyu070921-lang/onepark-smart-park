package model

import (
	"context"
	"sort"
	"time"

	"gorm.io/gorm"

	"onepark/app/energy-agent-service/internal/rules"
)

// EnergyReadingModel 只读 energy_reading 表。
// 这张表归 energy-data-service 写, 这里只查不改 —— 智能体是旁路, 不碰业务数据。
type EnergyReadingModel struct {
	db *gorm.DB
}

func NewEnergyReadingModel(db *gorm.DB) *EnergyReadingModel {
	return &EnergyReadingModel{db: db}
}

// ListZones 列出有过数据上报的所有区域
func (m *EnergyReadingModel) ListZones(ctx context.Context) ([]string, error) {
	var zones []string
	err := m.db.WithContext(ctx).
		Model(&EnergyReading{}).
		Distinct().
		Where("zone_id <> ''").
		Pluck("zone_id", &zones).Error
	return zones, err
}

// ListZoneDailyUsage 查某区域每天的用量, 日期从早到晚。
//
// 口径(和 M4 其他接口必须一致): 先按"每台设备每天"算 MAX-MIN, 再把这些差值加起来。
// 不能直接对整表 MAX-MIN, 那样会把不同电表的基数差算进去(比如 A 表从 10000 起跳,
// B 表从 50 起跳, 全表一减就凭空多出近万度)。
func (m *EnergyReadingModel) ListZoneDailyUsage(ctx context.Context, zoneID string, from, to time.Time) ([]rules.DayUsage, error) {
	q := `SELECT t.d AS date, SUM(t.usage_kwh) AS usage_kwh FROM (
			SELECT DATE(reported_at) AS d, device_id,
			       MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh
			FROM energy_reading
			WHERE zone_id = ? AND reported_at >= ? AND reported_at < ?
			GROUP BY d, device_id
		  ) AS t GROUP BY t.d ORDER BY t.d ASC`

	rows := make([]struct {
		Date     string  `gorm:"column:date"`
		UsageKwh float64 `gorm:"column:usage_kwh"`
	}, 0)

	if err := m.db.WithContext(ctx).Raw(q, zoneID, from, to).Scan(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]rules.DayUsage, 0, len(rows))
	for _, r := range rows {
		list = append(list, rules.DayUsage{Date: r.Date, Usage: r.UsageKwh})
	}
	return list, nil
}

// ListZoneHourlyUsage 查某区域某天的分时用量, 夜间空转判定要用
//
// ⚠️ 这里不能用"每个小时桶内 MAX-MIN 再相加" —— 那样会把跨小时的增量漏掉:
// 假设 23 点读到 10050、23 点半读到 10100, 而 9 点到 18 点各只有一条读数,
// 那么除了 23 点这个桶, 其他桶的差值全是 0, 算出来"夜间占 100%", 明显不对。
// (这个坑在计费的峰谷计价里踩过一次, 症状是账单少算钱。)
//
// 正确做法(差分法): 取每台设备每个小时桶的最后一条读数, 按时间排序后用
// "本桶读数 - 上一桶读数" 算出这一小时用了多少; 第一个桶以当天第一条读数做基准。
// 这样各小时之和正好等于当天总用量。
func (m *EnergyReadingModel) ListZoneHourlyUsage(ctx context.Context, zoneID string, day time.Time) ([]rules.HourUsage, error) {
	from := day
	to := day.AddDate(0, 0, 1)

	// 1. 每台设备在每个小时桶里的最后一条读数
	bucketSQL := "SELECT e.device_id, HOUR(e.reported_at) AS hour, e.energy_kwh " +
		"FROM energy_reading e " +
		"WHERE e.zone_id = ? AND e.reported_at >= ? AND e.reported_at < ? " +
		"AND e.reported_at = (SELECT MAX(r.reported_at) FROM energy_reading r " +
		"WHERE r.device_id = e.device_id AND r.zone_id = ? AND r.reported_at >= ? AND r.reported_at < ? " +
		"AND DATE_FORMAT(r.reported_at, '%Y-%m-%d %H') = DATE_FORMAT(e.reported_at, '%Y-%m-%d %H')) " +
		"ORDER BY e.device_id, e.reported_at ASC"

	buckets := make([]struct {
		DeviceID  string  `gorm:"column:device_id"`
		Hour      int     `gorm:"column:hour"`
		EnergyKwh float64 `gorm:"column:energy_kwh"`
	}, 0)
	if err := m.db.WithContext(ctx).Raw(bucketSQL, zoneID, from, to, zoneID, from, to).Scan(&buckets).Error; err != nil {
		return nil, err
	}
	if len(buckets) == 0 {
		return nil, nil
	}

	// 2. 每台设备当天的第一条读数, 作为差分的起点
	firstSQL := "SELECT e.device_id, e.energy_kwh FROM energy_reading e " +
		"WHERE e.zone_id = ? AND e.reported_at >= ? AND e.reported_at < ? " +
		"AND e.reported_at = (SELECT MIN(r.reported_at) FROM energy_reading r " +
		"WHERE r.device_id = e.device_id AND r.zone_id = ? AND r.reported_at >= ? AND r.reported_at < ?)"
	firsts := make([]struct {
		DeviceID  string  `gorm:"column:device_id"`
		EnergyKwh float64 `gorm:"column:energy_kwh"`
	}, 0)
	if err := m.db.WithContext(ctx).Raw(firstSQL, zoneID, from, to, zoneID, from, to).Scan(&firsts).Error; err != nil {
		return nil, err
	}
	firstOf := make(map[string]float64, len(firsts))
	for _, f := range firsts {
		firstOf[f.DeviceID] = f.EnergyKwh
	}

	// 3. 逐台设备做差分, 按小时累加
	prevOf := make(map[string]float64, len(buckets))
	hourSum := make(map[int]float64)
	for _, b := range buckets {
		prev, seen := prevOf[b.DeviceID]
		if !seen {
			// 这台设备的第一个桶: 拿当天第一条读数当基准,
			// 这样"第一条读数到第一个桶末尾"这段用量不会丢
			prev = firstOf[b.DeviceID]
		}
		delta := b.EnergyKwh - prev
		if delta > 0 { // 回退的负数不计入用量
			hourSum[b.Hour] += delta
		}
		prevOf[b.DeviceID] = b.EnergyKwh
	}

	list := make([]rules.HourUsage, 0, len(hourSum))
	for h, v := range hourSum {
		list = append(list, rules.HourUsage{Hour: h, Usage: v})
	}
	// 按钟点排序, 输出顺序稳定
	sort.Slice(list, func(i, j int) bool { return list[i].Hour < list[j].Hour })
	return list, nil
}

// DeviceDayUsage 一台设备一天的用量
type DeviceDayUsage struct {
	DeviceID string
	Usage    float64
}

// ListDeviceDailyUsage 查某区域某天每台设备各用了多少度
func (m *EnergyReadingModel) ListDeviceDailyUsage(ctx context.Context, zoneID string, day time.Time) ([]DeviceDayUsage, error) {
	from := day
	to := day.AddDate(0, 0, 1)

	rows := make([]struct {
		DeviceID string  `gorm:"column:device_id"`
		UsageKwh float64 `gorm:"column:usage_kwh"`
	}, 0)

	err := m.db.WithContext(ctx).
		Model(&EnergyReading{}).
		Select("device_id, MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh").
		Where("zone_id = ? AND reported_at >= ? AND reported_at < ?", zoneID, from, to).
		Group("device_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	list := make([]DeviceDayUsage, 0, len(rows))
	for _, r := range rows {
		list = append(list, DeviceDayUsage{DeviceID: r.DeviceID, Usage: r.UsageKwh})
	}
	return list, nil
}

// ListDeviceDailyUsageRange 查一台设备在一段时间里每天的用量, 用来算它自己的基线
func (m *EnergyReadingModel) ListDeviceDailyUsageRange(ctx context.Context, deviceID string, from, to time.Time) ([]rules.DayUsage, error) {
	rows := make([]struct {
		Date     string  `gorm:"column:date"`
		UsageKwh float64 `gorm:"column:usage_kwh"`
	}, 0)

	err := m.db.WithContext(ctx).
		Model(&EnergyReading{}).
		Select("DATE(reported_at) AS date, MAX(energy_kwh) - MIN(energy_kwh) AS usage_kwh").
		Where("device_id = ? AND reported_at >= ? AND reported_at < ?", deviceID, from, to).
		Group("date").
		Order("date ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	list := make([]rules.DayUsage, 0, len(rows))
	for _, r := range rows {
		list = append(list, rules.DayUsage{Date: r.Date, Usage: r.UsageKwh})
	}
	return list, nil
}

// ListBackwardDevices 找出某天出现"读数回退"的设备。
//
// 电表的累计读数正常情况下只增不减, 所以当天最后一条读数应该等于当天最大读数。
// 如果最后一条明显小于最大值, 说明中间出现过下降 —— 表计故障、换表或采集异常。
// 这类数据不可信, 必须单独报出来, 否则它算出的用量会偏小, 掩盖真实问题。
func (m *EnergyReadingModel) ListBackwardDevices(ctx context.Context, day time.Time) (map[string]bool, error) {
	from := day
	to := day.AddDate(0, 0, 1)

	rows := make([]struct {
		DeviceID string  `gorm:"column:device_id"`
		MaxKwh   float64 `gorm:"column:max_kwh"`
		LastKwh  float64 `gorm:"column:last_kwh"`
	}, 0)

	// 用 MAX(id) 近似"最后一条": id 自增, 入库顺序基本等于上报顺序
	q := `SELECT device_id, MAX(energy_kwh) AS max_kwh,
			(SELECT energy_kwh FROM energy_reading e2
			 WHERE e2.id = MAX(e1.id)) AS last_kwh
		  FROM energy_reading e1
		  WHERE reported_at >= ? AND reported_at < ?
		  GROUP BY device_id`

	if err := m.db.WithContext(ctx).Raw(q, from, to).Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[string]bool)
	for _, r := range rows {
		// 留 0.01 度的余量, 避免浮点误差造成误判
		if r.MaxKwh-r.LastKwh > 0.01 {
			out[r.DeviceID] = true
		}
	}
	return out, nil
}

// EnergyReading energy_reading 表结构(只读, 不建表不迁移)
type EnergyReading struct {
	ID         uint64    `gorm:"primaryKey;column:id"`
	DeviceID   string    `gorm:"column:device_id"`
	ZoneID     string    `gorm:"column:zone_id"`
	EnergyKwh  float64   `gorm:"column:energy_kwh"`
	PowerKw    *float64  `gorm:"column:power_kw"`
	ReportedAt time.Time `gorm:"column:reported_at"`
}

// TableName 表名
func (EnergyReading) TableName() string { return "energy_reading" }
