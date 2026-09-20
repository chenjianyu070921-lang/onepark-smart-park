package rules

// 基线计算与置信度/严重度映射。
//
// 基线是整套规则的基准, 算错了后面全错, 所以这块单独一个文件写清楚。

// MinBaselineDays 少于这么多天的有效历史就不算基线。
// 为什么是 3: 只有一两天的历史随机性太大, 拿它当基准会批量误报。
// 宁可这次不判定(等数据攒够), 也不能天天给运维推假警报。
const MinBaselineDays = 3

// Baseline 算历史日均用量。
//
// 关键点: 跳过用量为 0 的天。设备某天没上报, 用量算出来是 0,
// 如果把它算进均值会把基线拉低, 结果正常日子也显得"超基线" —— 典型误报来源。
//
// 返回 (基线值, 有效天数, 基线是否可用)
func Baseline(hist []DayUsage) (avg float64, days int, ok bool) {
	var sum float64
	for _, h := range hist {
		// 用量为 0 视为"没数据", 不进基线; 负数本身就是异常, 更不能进
		if h.Usage <= 0 {
			continue
		}
		sum += h.Usage
		days++
	}
	if days < MinBaselineDays {
		return 0, days, false
	}
	return sum / float64(days), days, true
}

// Ratio 实际值相对基线的倍数。基线为 0 时返回 0 表示"没法比"
func Ratio(actual, baseline float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return actual / baseline
}

// ConfidenceByRatio 按"超基线多少倍"算置信度。
// 偏离越大越可信: 刚好 1 倍(没超)是 0.5, 2 倍是 0.8, 3 倍以上封顶 0.95。
// 不封到 1.0 是因为规则判断永远不敢说 100% 确定。
func ConfidenceByRatio(ratio float64) float64 {
	c := 0.5 + (ratio-1)*0.3
	if c > 0.95 {
		c = 0.95
	}
	if c < 0 {
		c = 0
	}
	return c
}

// ConfidenceByExcess 按"超出阈值多少"算置信度, 给夜间占比这类"占比型"指标用。
// 比如阈值 25%, 实际 40%, 超出 15 个百分点 → 0.5 + 0.15*2 = 0.8
func ConfidenceByExcess(actual, threshold float64) float64 {
	c := 0.5 + (actual-threshold)*2
	if c > 0.95 {
		c = 0.95
	}
	if c < 0 {
		c = 0
	}
	return c
}

// SeverityByRatio 按倍数定严重程度: 2 倍以上高, 1.5 倍以上中, 其余低
func SeverityByRatio(ratio float64) int {
	switch {
	case ratio >= 2:
		return SeverityHigh
	case ratio >= 1.5:
		return SeverityMid
	default:
		return SeverityLow
	}
}

// NightUsage 统计夜间时段(23:00 到次日 06:00)的用量。
// 跨零点, 所以要单独处理: 23、0、1、2、3、4、5 点都算夜间。
func NightUsage(hours []HourUsage) (night, total float64) {
	for _, h := range hours {
		total += h.Usage
		if h.Hour >= NightStartHour || h.Hour < NightEndHour {
			night += h.Usage
		}
	}
	return night, total
}

// 夜间时段边界, 与 config 里的常量保持一致
const (
	NightStartHour = 23 // 含
	NightEndHour   = 6  // 不含
)

// Round2 保留两位小数, 避免浮点运算出现 0.30000000000000004 这种展示
func Round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
