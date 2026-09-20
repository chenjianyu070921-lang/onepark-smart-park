// Package calc 计费算法: 纯函数, 不碰数据库也不碰网络, 方便写单测
//
// 三种计价方式(对应 billing_rule.rule_type):
//
//	1 单一电价: 用了多少度 × 一个单价
//	2 阶梯电价: 把总用量切成几档, 每档按自己的单价算, 再相加
//	3 峰谷电价: 按用电的"钟点"分峰/平/谷, 不同钟点不同价
package calc

import (
	"math"
	"strconv"
)

// Tier 阶梯的一档
type Tier struct {
	// UpTo 本档的上限(度), nil 表示最后一档不封顶
	// 例: [UpTo=100, UpTo=300, UpTo=nil] 表示 0~100 / 100~300 / 300 以上
	UpTo  *float64
	Price float64
}

// HourUsage 某个钟点用了多少度(峰谷计价用)
// 例: Hour=8, Usage=12 表示"所有日子里早上 8 点这一个小时, 一共用了 12 度"
type HourUsage struct {
	Hour  int
	Usage float64
}

// Period 峰谷的一个时段
type Period struct {
	Name  string // 时段名, 如 "峰"
	From  string // 开始 "HH:MM"
	To    string // 结束 "HH:MM", 允许小于 From(表示跨零点, 如 23:00 → 07:00)
	Price float64
}

// DetailItem 账单明细的一行: 阶梯的某一档, 或峰谷的某个时段
type DetailItem struct {
	Name   string
	Usage  float64
	Price  float64
	Amount float64
}

// CalcFlat 单一电价: 金额 = 用量 × 单价
func CalcFlat(usage, price float64) float64 {
	if usage <= 0 || price <= 0 {
		return 0
	}
	return round2(usage * price)
}

// CalcTiered 阶梯累进计价
//
// 算法: 从第一档开始, 每一档最多装 (本档上限 - 上一档上限) 度, 装满就进入下一档,
// 最后剩下的全归不封顶的那一档。
//
// 例: 档位 0~100 @0.6、100~300 @0.8、300+ @1.0, 用了 350 度:
//
//	第一档 100 度 × 0.6 = 60
//	第二档 200 度 × 0.8 = 160
//	第三档  50 度 × 1.0 = 50
//	合计 270 元
func CalcTiered(usage float64, tiers []Tier) (float64, []DetailItem) {
	if usage <= 0 || len(tiers) == 0 {
		return 0, nil
	}

	var details []DetailItem
	var total float64
	// lower 是当前这一档的起点, used 是已经分掉的用量
	lower := 0.0
	used := 0.0

	for i, t := range tiers {
		if used >= usage {
			break
		}

		// 本档能装多少度: 有上限就装到上限, 没上限(最后一档)就把剩下的全装进去
		left := usage - used
		var take float64
		if t.UpTo == nil {
			take = left
		} else {
			capacity := *t.UpTo - lower
			if capacity <= 0 {
				// 档位配置有问题(后一档上限没比前一档大), 跳过这一档
				continue
			}
			take = math.Min(left, capacity)
		}
		if take <= 0 {
			continue
		}

		amount := round2(take * t.Price)
		total += amount
		details = append(details, DetailItem{
			Name:   tierName(lower, t.UpTo, i, len(tiers)),
			Usage:  round2(take),
			Price:  t.Price,
			Amount: amount,
		})

		used += take
		if t.UpTo != nil {
			lower = *t.UpTo
		}
	}

	return round2(total), details
}

// CalcTou 峰谷分时计价
//
// 算法: 把 24 个小时分别归到某个时段(匹配不上的按 base 平段价),
// 每个时段内把用量累加后乘自己的单价。
//
// 注意: 一天有 24 个小时, 但传进来的 HourUsage 是"整个账期内该钟点的总用量"
// (比如 30 天里每天早上 8 点的用量之和), 所以这里只做 24 次归类, 不会按天重复计算。
func CalcTou(hourly []HourUsage, base float64, periods []Period) (float64, []DetailItem) {
	if len(hourly) == 0 {
		return 0, nil
	}

	// 1. 先算出每个钟点归哪个时段、单价多少
	priceOf := make([]float64, 24)
	nameOf := make([]string, 24)
	for h := 0; h < 24; h++ {
		priceOf[h] = base
		nameOf[h] = "平"
		for _, p := range periods {
			if inPeriod(h, p) {
				priceOf[h] = p.Price
				nameOf[h] = p.Name
				break // 先匹配到的时段优先, 所以配规则时别让时段重叠
			}
		}
	}

	// 2. 按"时段名"把用量和金额攒起来(同一个名字可能覆盖多个钟点, 比如 8点/9点/10点都叫"峰")
	type bucket struct {
		usage  float64
		price  float64
		amount float64
	}
	buckets := make(map[string]*bucket)
	var order []string

	var total float64
	for _, hu := range hourly {
		if hu.Hour < 0 || hu.Hour > 23 || hu.Usage <= 0 {
			continue
		}
		name := nameOf[hu.Hour]
		price := priceOf[hu.Hour]
		amount := round2(hu.Usage * price)

		b, ok := buckets[name]
		if !ok {
			b = &bucket{price: price}
			buckets[name] = b
			order = append(order, name)
		}
		b.usage += hu.Usage
		b.amount += amount
		total += amount
	}

	details := make([]DetailItem, 0, len(order))
	for _, name := range order {
		b := buckets[name]
		details = append(details, DetailItem{
			Name:   name + "段",
			Usage:  round2(b.usage),
			Price:  b.price,
			Amount: round2(b.amount),
		})
	}

	return round2(total), details
}

// inPeriod 判断 hour 这个钟点是否落在时段内, 支持跨零点
// 例: 23:00→07:00, hour=23 或 hour=0~6 都算命中
func inPeriod(hour int, p Period) bool {
	from, ok1 := parseClock(p.From)
	to, ok2 := parseClock(p.To)
	if !ok1 || !ok2 {
		return false
	}
	if from == to {
		return false
	}
	if from < to {
		return hour >= from && hour < to
	}
	// 跨零点: 23:00→07:00 表示 23、0、1...6
	return hour >= from || hour < to
}

// parseClock 把 "08:00" 解析成 8
func parseClock(s string) (int, bool) {
	if len(s) < 5 {
		return 0, false
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h, true
}

// tierName 给明细行起个名字, 如 "第一档 0~100度"
func tierName(lower float64, upTo *float64, idx, total int) string {
	prefix := "第" + chineseNum(idx+1) + "档 "
	if upTo == nil {
		return prefix + "超过" + trimZero(lower) + "度"
	}
	return prefix + trimZero(lower) + "~" + trimZero(*upTo) + "度"
}

func chineseNum(n int) string {
	switch n {
	case 1:
		return "一"
	case 2:
		return "二"
	case 3:
		return "三"
	case 4:
		return "四"
	case 5:
		return "五"
	default:
		return "N"
	}
}

func trimZero(v float64) string {
	if v == math.Trunc(v) {
		return fmtInt(int(v))
	}
	return trimFloat(v)
}

func fmtInt(v int) string {
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

func trimFloat(v float64) string {
	// 最多保留两位小数, 去掉末尾多余的 0
	s := strconv.FormatFloat(v, 'f', 2, 64)
	for len(s) > 1 && s[len(s)-1] == '0' && s[len(s)-2] != '.' {
		s = s[:len(s)-1]
	}
	return s
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
