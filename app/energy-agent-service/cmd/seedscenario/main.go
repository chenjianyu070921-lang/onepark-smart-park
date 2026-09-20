// 合成场景造数工具: 往 energy_reading 注入"已知正确答案"的异常场景, 用来验证智能体。
//
// 为什么需要它: 真实园区没有历史故障标签, 光看数据不知道"这里本该报还是不该报",
// 就没法判断智能体干得好不好。这里自己造六个场景, 每个场景在造的时候就知道
// 正确答案是什么(见下面的 expected), 天然自带标注 —— 这是评测集最省事的建法。
//
// 用法:
//
//	go run ./cmd/seedscenario            # 造数据(默认统计日 2026-09-15)
//	go run ./cmd/seedscenario -clean     # 只清理, 不造数
//
// 造完之后调 POST /api/agent/inspect {"statDate":"2026-09-15"},
// 看它报出来的和 expected 差多少。
package main

import (
	"flag"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 六个场景的区域名。用独立前缀, 不污染真实的 A栋/B栋 数据
const (
	zoneNormal  = "SCEN-正常区" // 什么都不该报
	zoneNight   = "SCEN-空转区" // 应报: 夜间空转
	zoneSurge   = "SCEN-普涨区" // 应报: 区域用量普涨
	zoneSpike   = "SCEN-突增区" // 应报: 单设备突增(区域总量不变, 只有一台暴涨)
	zoneMissing = "SCEN-缺失区" // 应报: 数据缺失
	zoneBack    = "SCEN-回退区" // 应报: 表计读数回退
)

// expected 每个区域的正确答案, 造数时一并写死, 用来对答案。
// 实测(2026-09-16)智能体 6 个场景全中, 正常区零误报。
var expected = map[string]string{
	zoneNormal: "无发现",
	zoneNight:  "night_idle(夜间空转)",
	// 普涨区会报两条: 区域级普涨 + 设备级突增。不重复计数 ——
	// 区域涨是因为它下面那台设备涨了, 两条指向同一个根因, 设备那条负责精确定位
	zoneSurge:   "zone_surge(区域普涨) + device_spike(该区设备也超基线)",
	zoneSpike:   "device_spike(单设备突增, 应精确指向 -02 那台)",
	zoneMissing: "data_missing(数据缺失)",
	zoneBack:    "meter_backward(读数回退)",
}

const (
	baseDays     = 7            // 基线期天数
	statDay      = "2026-09-15" // 统计日
	baseStartDay = "2026-09-08" // 基线期起点
	baseUsage    = 100.0        // 基线期每天用量
	dsn          = "root:4ay1nkal3u8ed77y@tcp(115.191.16.159:3306)/onepark-smart-park?charset=utf8mb4&parseTime=true&loc=Local"
)

// point 一天中的一个采集点: hour 是钟点, delta 是相对当天起点的累计增量
type point struct {
	hour  int
	delta float64
}

// 基线期每天的用电曲线: 全天 100 度, 夜间(23点/0点/1点)只占 5 度 → 5%
var normalDay = []point{
	{hour: 0, delta: 0},
	{hour: 1, delta: 5},
	{hour: 9, delta: 50},
	{hour: 18, delta: 100},
}

// 夜间空转: 全天还是 100 度, 但 80 度发生在深夜 → 占比 80%(阈值 25%)
var nightDay = []point{
	{hour: 0, delta: 0},
	{hour: 1, delta: 30},
	{hour: 9, delta: 35},
	{hour: 12, delta: 40},
	{hour: 23, delta: 50},
	{hour: 23, delta: 100}, // 同一小时再来一条, 模拟深夜大功率
}

// 区域普涨: 220 度, 是基线 100 的 2.2 倍
var surgeDay = []point{
	{hour: 0, delta: 0},
	{hour: 1, delta: 10},
	{hour: 9, delta: 110},
	{hour: 18, delta: 220},
}

// 读数回退: 中午的读数比早上还低(表计故障), 最后也没回到最高点
var backDay = []point{
	{hour: 0, delta: 0},
	{hour: 9, delta: 50},
	{hour: 12, delta: 30}, // 这里回退了
	{hour: 18, delta: 40},
}

type reading struct {
	DeviceID   string    `gorm:"column:device_id"`
	ZoneID     string    `gorm:"column:zone_id"`
	EnergyKwh  float64   `gorm:"column:energy_kwh"`
	ReportedAt time.Time `gorm:"column:reported_at"`
}

func (reading) TableName() string { return "energy_reading" }

func main() {
	clean := flag.Bool("clean", false, "只清理场景数据, 不造数")
	flag.Parse()

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Println("连库失败:", err)
		return
	}

	// 先清掉旧的场景数据, 保证重复运行结果一致
	zones := []string{zoneNormal, zoneNight, zoneSurge, zoneSpike, zoneMissing, zoneBack}
	if err := db.Where("zone_id IN ?", zones).Delete(&reading{}).Error; err != nil {
		fmt.Println("清理失败:", err)
		return
	}
	// 智能体的产出也一起清掉, 否则每次跑完建议会越攒越多, 分不清哪条是这次的
	for _, t := range []string{"agent_suggestion", "agent_tool_call", "agent_run"} {
		if err := db.Exec("DELETE FROM " + t).Error; err != nil {
			fmt.Println("清理"+t+"失败:", err)
		}
	}
	fmt.Println("已清理旧的场景数据与智能体产出")
	if *clean {
		return
	}

	statDate := mustDay(statDay)
	startDay := mustDay(baseStartDay)

	// ---- 正常区 / 空转区 / 普涨区 / 缺失区 / 回退区: 各一台设备 ----
	// 基线期每天都用 100 度
	for _, z := range []string{zoneNormal, zoneNight, zoneSurge, zoneMissing, zoneBack} {
		dev := z + "-01"
		kwh := writeDays(db, dev, z, startDay, baseDays, normalDay, 1000)
		// 统计日各自的剧本
		switch z {
		case zoneNormal:
			// 105 度, 比基线高 5%, 属正常波动
			writeOneDay(db, dev, z, statDate, kwh, []point{
				{hour: 0, delta: 0}, {hour: 1, delta: 5}, {hour: 9, delta: 55}, {hour: 18, delta: 105},
			})
		case zoneNight:
			writeOneDay(db, dev, z, statDate, kwh, nightDay)
		case zoneSurge:
			writeOneDay(db, dev, z, statDate, kwh, surgeDay)
		case zoneBack:
			writeOneDay(db, dev, z, statDate, kwh, backDay)
		case zoneMissing:
			// 什么都不写: 基线期一直有数据, 统计日没有 → 触发数据缺失
		}
	}

	// ---- 突增区: 两台设备, 总量不变但其中一台暴涨 ----
	// 基线期各 50 度(区域共 100)
	k1 := writeDays(db, zoneSpike+"-01", zoneSpike, startDay, baseDays, []point{
		{hour: 0, delta: 0}, {hour: 9, delta: 25}, {hour: 18, delta: 50},
	}, 2000)
	k2 := writeDays(db, zoneSpike+"-02", zoneSpike, startDay, baseDays, []point{
		{hour: 0, delta: 0}, {hour: 9, delta: 25}, {hour: 18, delta: 50},
	}, 3000)
	// 统计日: 01 号骤降到 5 度, 02 号涨到 95 度(是自己基线 50 的 1.9 倍)
	// 区域总量仍是 100, 所以不该报"区域普涨", 只报"单设备突增"
	writeOneDay(db, zoneSpike+"-01", zoneSpike, statDate, k1, []point{
		{hour: 0, delta: 0}, {hour: 9, delta: 3}, {hour: 18, delta: 5},
	})
	writeOneDay(db, zoneSpike+"-02", zoneSpike, statDate, k2, []point{
		{hour: 0, delta: 0}, {hour: 9, delta: 50}, {hour: 18, delta: 95},
	})

	fmt.Printf("\n造数完成, 统计日 %s, 基线期 %s 起 %d 天\n\n", statDay, baseStartDay, baseDays)
	fmt.Println("各区域的正确答案(拿它跟智能体报出来的对):")
	for _, z := range zones {
		fmt.Printf("  %-14s → %s\n", z, expected[z])
	}
	fmt.Println("\n下一步: 启动服务后调 POST /api/agent/inspect {\"statDate\":\"2026-09-15\"}")
}

// writeDays 写基线期若干天的正常数据, 返回最后一天结束时的累计读数
func writeDays(db *gorm.DB, dev, zone string, start time.Time, days int, pts []point, startKwh float64) float64 {
	kwh := startKwh
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		kwh = writeOneDay(db, dev, zone, day, kwh, pts)
	}
	return kwh
}

// writeOneDay 写一天的数据, 返回这天结束时的累计读数
func writeOneDay(db *gorm.DB, dev, zone string, day time.Time, startKwh float64, pts []point) float64 {
	last := startKwh
	for i, p := range pts {
		t := day.Add(time.Duration(p.hour) * time.Hour)
		// 同一小时内有多条时错开分钟, 保证时间不重复
		t = t.Add(time.Duration(i) * time.Minute)
		r := reading{
			DeviceID:   dev,
			ZoneID:     zone,
			EnergyKwh:  startKwh + p.delta,
			ReportedAt: t,
		}
		if err := db.Create(&r).Error; err != nil {
			fmt.Println("插入失败:", err)
		}
		last = startKwh + p.delta
	}
	return last
}

func mustDay(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}
