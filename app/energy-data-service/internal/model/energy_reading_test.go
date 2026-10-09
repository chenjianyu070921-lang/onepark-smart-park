package model

import (
	"context"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/energy-data-service/internal/config"
)

// 测试数据统一用 UTM- 前缀 + UT区, 跑前跑后各清一次, 不碰真实数据
//
// 前缀必须每个测试包各用一个(model 用 UTM-, server 用 UTS-):
// go test 默认**按包并行**, 两个包如果共用同一个 LIKE 'UT-%' 的清理语句,
// 就会互相删对方的种子数据, 表现为"刚写完就查不到、轮询也等不回来"(踩过)。
//
// 连不上库(没网/没环境)就整文件跳过, 免得单元测试红一片

func newTestDB(t *testing.T) *gorm.DB {
	var c config.Config
	if err := conf.Load("../../etc/energydata-api.yaml", &c); err != nil {
		t.Skipf("读不到配置文件, 跳过: %v", err)
	}
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		t.Skipf("连不上数据库, 跳过: %v", err)
	}
	cleanUT(db)
	t.Cleanup(func() { cleanUT(db) })
	return db
}

func cleanUT(db *gorm.DB) {
	db.Exec("DELETE FROM energy_reading WHERE device_id LIKE 'UTM-%'")
}

func seed(t *testing.T, db *gorm.DB, device, zone string, kwh float64, at time.Time) {
	t.Helper()
	r := &EnergyReading{DeviceID: device, ZoneID: zone, EnergyKwh: kwh, ReportedAt: at}
	if err := db.Create(r).Error; err != nil {
		t.Fatalf("造数据失败: %v", err)
	}
}

var (
	utDay     = time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local) // 固定用 2026-09-10, 结果可预期
	utNextDay = utDay.AddDate(0, 0, 1)
	utLayout  = "2006-01-02 15:00"
)

// TestInsertAndFindLatest 写入后能查到最新一条
func TestInsertAndFindLatest(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)
	ctx := context.Background()

	seed(t, db, "UTM-1", "UT区", 110, utDay.Add(10*time.Minute))
	seed(t, db, "UTM-1", "UT区", 150, utDay.Add(8*time.Hour))
	seed(t, db, "UTM-1", "UT区", 180, utDay.Add(23*time.Hour+50*time.Minute))

	latest, err := m.FindLatest(ctx, "UTM-1")
	if err != nil {
		t.Fatalf("FindLatest 失败: %v", err)
	}
	if latest.EnergyKwh != 180 {
		t.Errorf("最新读数应该是 180, 实际 %v", latest.EnergyKwh)
	}
}

// TestBatchInsert 攒批写入和单条写入效果一致
func TestBatchInsert(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)
	ctx := context.Background()

	rows := []*EnergyReading{
		{DeviceID: "UTM-2", ZoneID: "UT区", EnergyKwh: 5000, ReportedAt: utDay.Add(30 * time.Minute)},
		{DeviceID: "UTM-2", ZoneID: "UT区", EnergyKwh: 5010, ReportedAt: utDay.Add(12 * time.Hour)},
	}
	if err := m.BatchInsert(ctx, rows); err != nil {
		t.Fatalf("BatchInsert 失败: %v", err)
	}
	latest, err := m.FindLatest(ctx, "UTM-2")
	if err != nil {
		t.Fatalf("批量写入后查不到数据: %v", err)
	}
	if latest.EnergyKwh != 5010 {
		t.Errorf("批量写入的最新读数应该是 5010, 实际 %v", latest.EnergyKwh)
	}
}

// TestUsageBetween 用量 = 窗口内 MAX-MIN; 期初之前的读数和期末都不算
func TestUsageBetween(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)
	ctx := context.Background()

	// 前一天 23:50 的读数是期初, 不在窗口内
	seed(t, db, "UTM-1", "UT区", 100, utDay.Add(-10*time.Minute))
	seed(t, db, "UTM-1", "UT区", 110, utDay.Add(10*time.Minute))
	seed(t, db, "UTM-1", "UT区", 150, utDay.Add(8*time.Hour))
	seed(t, db, "UTM-1", "UT区", 180, utDay.Add(23*time.Hour+50*time.Minute))

	usage, err := m.UsageBetween(ctx, "UTM-1", utDay, utNextDay)
	if err != nil {
		t.Fatalf("UsageBetween 失败: %v", err)
	}
	if usage != 70 { // 180 - 110
		t.Errorf("整天用量应该是 70, 实际 %v", usage)
	}

	// 右边界不含: 截到 23:50 前面, 用量 = 150 - 110 = 40
	usage, err = m.UsageBetween(ctx, "UTM-1", utDay, utDay.Add(23*time.Hour+50*time.Minute))
	if err != nil {
		t.Fatalf("UsageBetween 失败: %v", err)
	}
	if usage != 40 {
		t.Errorf("截止 23:50 前的用量应该是 40, 实际 %v", usage)
	}

	// 没数据的设备返回 0 而不是报错
	usage, err = m.UsageBetween(ctx, "UT-无数据", utDay, utNextDay)
	if err != nil || usage != 0 {
		t.Errorf("没数据应该返回 0,nil, 实际 %v,%v", usage, err)
	}
}

// pollTotal 断言前轮询等数据可见(最多 15 秒)。
// 公用库是远程共享库, 偶发慢查询/抖动, 轮询比一次性断言稳, 失败时还能打印表里实际行数方便排障
func pollTotal(t *testing.T, db *gorm.DB, m *EnergyReadingModel, want float64, zone string, start, end time.Time) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		total, err := m.TotalUsageByDevice(context.Background(), start, end, zone)
		if err == nil && total == want {
			return
		}
		if time.Now().After(deadline) {
			var n int64
			db.Model(&EnergyReading{}).Where("device_id LIKE 'UTM-%'").Count(&n)
			t.Fatalf("等了 15 秒总量还不是 %v (最后读到 %v, err=%v, 表里 UT 行数=%d)", want, total, err, n)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestTotalUsageByDevice 必须先按设备算差值再求和。
// 两台设备基数差 4900 度, 如果写成全表 MAX-MIN 就会得出 5010-110=4900, 而不是 80。
func TestTotalUsageByDevice(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)

	seed(t, db, "UTM-1", "UT区", 110, utDay.Add(10*time.Minute))
	seed(t, db, "UTM-1", "UT区", 180, utDay.Add(23*time.Hour+50*time.Minute)) // 70
	seed(t, db, "UTM-2", "UT区", 5000, utDay.Add(30*time.Minute))
	seed(t, db, "UTM-2", "UT区", 5010, utDay.Add(12*time.Hour)) // 10

	pollTotal(t, db, m, 80, "UT区", utDay, utNextDay)

	// 区域过滤: 只看 UT区 时不能把其他区域算进来
	seed(t, db, "UTM-9", "别区", 9000, utDay.Add(1*time.Hour))
	seed(t, db, "UTM-9", "别区", 9500, utDay.Add(2*time.Hour))
	pollTotal(t, db, m, 80, "UT区", utDay, utNextDay)
}

// TestListUsageDifferential 差分法: 相邻读数的增量记到后一条所在的小时桶。
// 关键点: 稀疏上报时增量跨桶(08:50→09:10 的 10 度要记进 09:00 桶),
// 读数回退(200→190)按 0 记, 不产生负数; 各桶之和 = 区间总量。
func TestListUsageDifferential(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)
	ctx := context.Background()

	// 时间线(UTM-3): 08:50=100, 09:10=110, 09:40=110.5, 10:05=170, 11:00=200, 11:30=190(回退), 12:00=195
	type pt struct {
		min int
		kwh float64
	}
	for _, p := range []pt{
		{8*60 + 50, 100}, {9*60 + 10, 110}, {9*60 + 40, 110.5},
		{10*60 + 5, 170}, {11 * 60, 200}, {11*60 + 30, 190}, {12 * 60, 195},
	} {
		seed(t, db, "UTM-3", "UT区", p.kwh, utDay.Add(time.Duration(p.min)*time.Minute))
	}

	// 轮询等种子数据可见(远程共享库偶发抖动, 比一次性断言稳)
	var points []UsagePoint
	deadline := time.Now().Add(15 * time.Second)
	for {
		var err error
		points, err = m.ListUsage(ctx, "UTM-3", utDay, utNextDay, utLayout)
		if err != nil {
			t.Fatalf("ListUsage 失败: %v", err)
		}
		if len(points) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等了 15 秒还是没读到 4 个桶, 实际 %d 个: %+v", len(points), points)
		}
		time.Sleep(300 * time.Millisecond)
	}

	want := map[string]float64{
		"2026-09-10 09:00": 10.5, // 10 + 0.5, 跨桶增量记到后一条所在桶
		"2026-09-10 10:00": 59.5, // 170 - 110.5
		"2026-09-10 11:00": 30,   // 200-170; 回退 190-200<0 按 0
		"2026-09-10 12:00": 5,    // 195-190
	}
	if len(points) != len(want) {
		t.Fatalf("应该有 %d 个桶, 实际 %d 个: %+v", len(want), len(points), points)
	}
	sum := 0.0
	for _, p := range points {
		w, ok := want[p.Bucket]
		if !ok {
			t.Errorf("多出来的桶 %s = %v", p.Bucket, p.Usage)
			continue
		}
		if p.Usage != w {
			t.Errorf("桶 %s 应该是 %v, 实际 %v", p.Bucket, w, p.Usage)
		}
		sum += p.Usage
	}
	if sum != 105 { // 10.5+59.5+30+5, 等于区间总量(扣掉回退)
		t.Errorf("各桶之和应该等于区间总量 105, 实际 %v", sum)
	}
}

// TestFindLatestAny 最新读数要能按区域过滤
func TestFindLatestAny(t *testing.T) {
	db := newTestDB(t)
	m := NewEnergyReadingModel(db)
	ctx := context.Background()

	seed(t, db, "UTM-1", "UT区", 180, utDay.Add(23*time.Hour+50*time.Minute))
	seed(t, db, "UTM-2", "别区", 5010, utDay.Add(23*time.Hour))

	r, err := m.FindLatestAny(ctx, "UT区")
	if err != nil {
		t.Fatalf("FindLatestAny 失败: %v", err)
	}
	if r.DeviceID != "UTM-1" {
		t.Errorf("UT区 最新一条应该是 UTM-1 的, 实际 %s", r.DeviceID)
	}
}
