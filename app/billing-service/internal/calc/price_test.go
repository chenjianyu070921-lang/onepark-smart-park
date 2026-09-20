package calc

import (
	"math"
	"testing"
)

func fptr(v float64) *float64 { return &v }

// almost 判断两个金额是否相等, 浮点数直接比 == 不稳, 允许 1 分钱误差
func almost(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestCalcFlat(t *testing.T) {
	cases := []struct {
		name  string
		usage float64
		price float64
		want  float64
	}{
		{"正常计费", 100, 0.65, 65},
		{"零用量", 0, 0.65, 0},
		{"零单价", 100, 0, 0},
		{"小数用量", 12.5, 0.8, 10},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CalcFlat(c.usage, c.price)
			if !almost(got, c.want) {
				t.Errorf("CalcFlat(%v, %v) = %v, 想要 %v", c.usage, c.price, got, c.want)
			}
		})
	}
}

func TestCalcTiered(t *testing.T) {
	// 0~100 度 0.6 元, 100~300 度 0.8 元, 300 度以上 1.0 元
	tiers := []Tier{
		{UpTo: fptr(100), Price: 0.6},
		{UpTo: fptr(300), Price: 0.8},
		{UpTo: nil, Price: 1.0},
	}

	cases := []struct {
		name  string
		usage float64
		want  float64
	}{
		{"只落在第一档", 50, 30},      // 50 × 0.6
		{"正好卡在第一档上限", 100, 60},  // 100 × 0.6, 边界值
		{"刚过第一档", 101, 60.8},    // 100×0.6 + 1×0.8
		{"横跨三档", 350, 270},      // 100×0.6 + 200×0.8 + 50×1.0
		{"正好卡在第二档上限", 300, 220}, // 100×0.6 + 200×0.8
		{"零用量", 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, details := CalcTiered(c.usage, tiers)
			if !almost(got, c.want) {
				t.Errorf("CalcTiered(%v) = %v, 想要 %v, 明细=%+v", c.usage, got, c.want, details)
			}
			// 明细里每一档的用量加起来, 应该正好等于总用量
			var sum float64
			for _, d := range details {
				sum += d.Usage
			}
			if c.usage > 0 && !almost(sum, c.usage) {
				t.Errorf("明细用量之和 %v 与总用量 %v 对不上", sum, c.usage)
			}
		})
	}
}

func TestCalcTieredNoTier(t *testing.T) {
	if got, _ := CalcTiered(100, nil); got != 0 {
		t.Errorf("没配档位应该返回 0, 实际 %v", got)
	}
}

func TestCalcTou(t *testing.T) {
	// 峰 8:00-11:00 = 1.05 元, 谷 23:00-07:00(跨零点) = 0.35 元, 其余平段 0.6 元
	periods := []Period{
		{Name: "峰", From: "08:00", To: "11:00", Price: 1.05},
		{Name: "谷", From: "23:00", To: "07:00", Price: 0.35},
	}
	const base = 0.6

	hourly := []HourUsage{
		{Hour: 8, Usage: 10},  // 峰 10 × 1.05 = 10.5
		{Hour: 9, Usage: 10},  // 峰 10 × 1.05 = 10.5
		{Hour: 2, Usage: 5},   // 谷 5 × 0.35 = 1.75
		{Hour: 15, Usage: 20}, // 平 20 × 0.6 = 12
	}
	// 合计 10.5 + 10.5 + 1.75 + 12 = 34.75

	got, details := CalcTou(hourly, base, periods)
	if !almost(got, 34.75) {
		t.Errorf("CalcTou() = %v, 想要 34.75, 明细=%+v", got, details)
	}

	// 明细应该有 3 条: 峰段/平段/谷段
	if len(details) != 3 {
		t.Fatalf("明细应该有 3 段(峰/平/谷), 实际 %d 条: %+v", len(details), details)
	}

	byName := make(map[string]DetailItem)
	for _, d := range details {
		byName[d.Name] = d
	}
	// 峰段: 8点和9点共 20 度, 21 元
	if !almost(byName["峰段"].Usage, 20) || !almost(byName["峰段"].Amount, 21) {
		t.Errorf("峰段算错了: %+v", byName["峰段"])
	}
	// 谷段: 凌晨 2 点 5 度, 1.75 元
	if !almost(byName["谷段"].Usage, 5) || !almost(byName["谷段"].Amount, 1.75) {
		t.Errorf("谷段算错了: %+v", byName["谷段"])
	}
	// 平段: 下午 3 点 20 度, 12 元
	if !almost(byName["平段"].Usage, 20) || !almost(byName["平段"].Amount, 12) {
		t.Errorf("平段算错了: %+v", byName["平段"])
	}
}

func TestInPeriod(t *testing.T) {
	p := Period{Name: "谷", From: "23:00", To: "07:00", Price: 0.35}

	cases := []struct {
		hour int
		want bool
	}{
		{23, true},  // 起点
		{0, true},   // 跨过零点
		{6, true},   // 终点前一小时
		{7, false},  // 终点本身不算
		{12, false}, // 白天
	}

	for _, c := range cases {
		if got := inPeriod(c.hour, p); got != c.want {
			t.Errorf("inPeriod(%d点, 23:00-07:00) = %v, 想要 %v", c.hour, got, c.want)
		}
	}
}

func TestParseClock(t *testing.T) {
	if h, ok := parseClock("08:30"); !ok || h != 8 {
		t.Errorf("parseClock(08:30) = %v, %v, 想要 8, true", h, ok)
	}
	if _, ok := parseClock("乱码"); ok {
		t.Error("非法时间应该返回 false")
	}
	if _, ok := parseClock("25:00"); ok {
		t.Error("25 点应该返回 false")
	}
}
