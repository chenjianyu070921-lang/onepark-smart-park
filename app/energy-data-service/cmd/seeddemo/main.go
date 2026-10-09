// 演示数据填充工具: 给 A/B/C 栋补齐整个 9 月的小时级读数, 让看板不再单调。
//
// 关键设计:
//   - 累计读数必须随时间单调不减, 已有的真实/演示数据点一律作为锚点保留,
//     新生成的点在锚点之间按"昼夜形状"分配增量, 保证差分法算出的曲线和总量自洽
//   - 已有读数前后 45 分钟内不重复生成, 不碰不覆盖
//   - 用量按"白天高夜间低 + 周末偏低"的形状分布, 看板上的 24 小时柱状图才像真的
//
// 用法: go run ./cmd/seeddemo
package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/energy-data-service/internal/config"
	"onepark/app/energy-data-service/internal/model"
)

// deviceSpec 一台演示设备的画像
type deviceSpec struct {
	id        string
	zone      string
	startKwh  float64 // 9月1日 00:00 的累计读数(对已有数据的设备, 必须小于它最早一条读数)
	kwhPerDay float64 // 日均用量
	baseKw    float64 // 实时功率基准
}

var specs = []deviceSpec{
	// A栋: 商办楼, 用量最大
	{"METER-A01", "A栋", 11380, 150, 8.5}, // 已有 9-15 12000~12010, 作锚点保留
	{"METER-A02", "A栋", 6210, 170, 9.2},
	{"METER-A03", "A栋", 903, 105, 5.6},
	// B栋: 宿舍楼, 日均小一些
	{"METER-B01", "B栋", 7590, 115, 6.0}, // 已有 9-15 8000~8010
	{"METER-B02", "B栋", 2115, 135, 7.1},
	{"METER-B03", "B栋", 298, 68, 3.5},
	// C栋: 配套商业, 中等
	{"METER-C01", "C栋", 1502, 96, 5.0},
	{"MOCK-01", "C栋", 4970, 2.2, 3.5}, // 已有 5000→5015 稀疏锚点
	{"MOCK-02", "C栋", 4971, 2.2, 3.4},
}

var hourWeights = []float64{
	0.30, 0.25, 0.22, 0.20, 0.25, 0.35, // 0-5 深夜
	0.60, 1.10, 1.80, 2.20, 2.30, 2.10, // 6-11 上班爬坡
	1.60, 1.40, 2.00, 2.20, 2.10, 1.90, // 12-17 午休小凹
	1.70, 1.40, 1.10, 0.90, 0.60, 0.40, // 18-23 收班
}

func hourWeight(h int) float64 { return hourWeights[h] }

// shapeWeight 小时基础权重 * 周末系数(周末办公楼用电降下来)
func shapeWeight(t time.Time) float64 {
	w := hourWeight(t.Hour())
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		w *= 0.65
	}
	return w
}

type anchor struct {
	t   time.Time
	kwh float64
}

func main() {
	var c config.Config
	conf.MustLoad("etc/energydata-api.yaml", &c)
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	m := model.NewEnergyReadingModel(db)

	sep1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	now := time.Now().Truncate(time.Hour) // 补到当前整点, 今天也有数据

	total := 0
	for _, s := range specs {
		n, err := seedDevice(ctx, db, m, s, sep1, now)
		if err != nil {
			fmt.Printf("%s 失败: %v\n", s.id, err)
			continue
		}
		total += n
		fmt.Printf("%-12s zone=%-4s 补了 %d 条\n", s.id, s.zone, n)
	}
	fmt.Printf("合计 %d 条\n", total)
}

// seedDevice 给一台设备补数, 返回插入条数
func seedDevice(ctx context.Context, db *gorm.DB, m *model.EnergyReadingModel,
	s deviceSpec, sep1, now time.Time) (int, error) {

	// 1. 拉该设备已有读数, 当锚点
	type row struct {
		T   time.Time
		Kwh float64
	}
	var exist []row
	if err := db.WithContext(ctx).Raw(
		`SELECT reported_at AS t, energy_kwh AS kwh FROM energy_reading
		 WHERE device_id = ? AND reported_at >= ? ORDER BY reported_at`, s.id, sep1).Scan(&exist).Error; err != nil {
		return 0, err
	}

	// 2. 组锚点: 起点 + 已有点 + 终点
	anchors := []anchor{{sep1, s.startKwh}}
	for _, e := range exist {
		anchors = append(anchors, anchor{e.T, e.Kwh})
	}
	lastKwh, lastT := s.startKwh, sep1
	if len(exist) > 0 {
		lastKwh = exist[len(exist)-1].Kwh
		lastT = exist[len(exist)-1].T
	}
	endKwh := lastKwh + s.kwhPerDay*now.Sub(lastT).Hours()/24
	anchors = append(anchors, anchor{now, math.Round(endKwh*10) / 10})

	// 锚点必须严格单调, 不然差分会出负数
	for i := 1; i < len(anchors); i++ {
		if anchors[i].kwh <= anchors[i-1].kwh {
			anchors[i].kwh = anchors[i-1].kwh + 0.1
		}
	}

	// 3. 逐小时生成
	var rows []*model.EnergyReading
	prevKwh := math.Inf(-1)
	for t := sep1.Add(time.Hour); !t.After(now); t = t.Add(time.Hour) {
		// 离已有读数太近的跳过, 别重复
		near := false
		for _, e := range exist {
			if math.Abs(e.T.Sub(t).Minutes()) < 45 {
				near = true
				break
			}
		}
		if near {
			continue
		}

		kwh := interp(anchors, t)
		if kwh <= prevKwh {
			kwh = prevKwh + 0.01 // 保底单调
		}
		prevKwh = kwh

		kw := s.baseKw * (0.4 + 0.9*hourWeight(t.Hour())/2.3)
		rows = append(rows, &model.EnergyReading{
			DeviceID:   s.id,
			ZoneID:     s.zone,
			EnergyKwh:  math.Round(kwh*10) / 10,
			PowerKw:    &kw,
			ReportedAt: t,
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := m.BatchInsert(ctx, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// interp 在锚点之间做分段线性插值, 增量按"昼夜形状权重"分配:
// t 时刻的读数 = 区间起点读数 + 区间总增量 × (起点到 t 的累计权重 / 区间总权重),
// 这样曲线贴合作息形状, 且严格落在相邻锚点读数之间(保单调)。
func interp(anchors []anchor, t time.Time) float64 {
	i := 0
	for i < len(anchors)-2 && !anchors[i+1].t.After(t) {
		i++
	}
	a, b := anchors[i], anchors[i+1]
	if !t.After(a.t) {
		return a.kwh
	}
	if !t.Before(b.t) {
		return b.kwh
	}

	totalW, cumW := 0.0, 0.0
	for u := a.t.Add(time.Hour); !u.After(b.t); u = u.Add(time.Hour) {
		w := shapeWeight(u)
		totalW += w
		if !u.After(t) {
			cumW += w
		}
	}
	if totalW == 0 {
		return a.kwh + (b.kwh-a.kwh)*t.Sub(a.t).Minutes()/b.t.Sub(a.t).Minutes()
	}
	return a.kwh + (b.kwh-a.kwh)*(cumW/totalW)
}
