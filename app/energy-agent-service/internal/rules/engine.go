package rules

import "sort"

// Input 一次巡检的全部输入。数据由调用方(model 层)查好传进来,
// rules 层只做计算, 这样它才是纯的、可单测的
type Input struct {
	StatDate string        // 统计哪天, 2006-01-02
	Zones    []ZoneInput   // 各区域的用量与基线数据
	Devices  []DeviceInput // 各设备的用量与基线数据
}

// MaxDeviceFindingsPerZone 每个区域最多报几条设备级发现。
// 一个区域几十台设备, 全报出来运维根本看不过来, 只留偏离最大的前几台。
const MaxDeviceFindingsPerZone = 3

// Analyze 跑完整的规则归因, 返回所有发现(已排序)。
//
// 编排顺序:
//  1. 区域级: 数据缺失 → 用量普涨 → 夜间空转
//  2. 设备级: 读数回退 → 单设备突增(每个区域只留偏离最大的前几台)
//  3. 统一排序: 严重程度高的在前, 同等严重程度下置信度高的在前
func Analyze(in Input, t Threshold) []Finding {
	var out []Finding

	// ---- 区域级 ----
	for _, z := range in.Zones {
		if f, ok := DetectDataMissing(z.ZoneID, "", z.HasData, hasHistory(z.History)); ok {
			out = append(out, f)
			continue // 没数据就没法算别的了, 跳过后续判定
		}
		if f, ok := DetectZoneSurge(z, t); ok {
			out = append(out, f)
		}
		if f, ok := DetectNightIdle(z, t); ok {
			out = append(out, f)
		}
	}

	// ---- 设备级 ----
	// 读数回退是数据质量问题, 优先报, 且不受 Top N 限制
	for _, d := range in.Devices {
		if f, ok := DetectMeterBackward(d); ok {
			out = append(out, f)
		}
		if f, ok := DetectDataMissing(d.ZoneID, d.DeviceID, d.HasData, hasHistory(d.History)); ok {
			out = append(out, f)
		}
	}
	// 单设备突增按区域分组, 每组只留前几名
	for _, z := range in.Zones {
		var spikes []Finding
		for _, d := range in.Devices {
			if d.ZoneID != z.ZoneID {
				continue
			}
			if f, ok := DetectDeviceSpike(d, t); ok {
				spikes = append(spikes, f)
			}
		}
		// 按"多用了多少度"排序, 影响最大的排前面
		sort.Slice(spikes, func(i, j int) bool {
			return spikes[i].Evidence["delta"] > spikes[j].Evidence["delta"]
		})
		if len(spikes) > MaxDeviceFindingsPerZone {
			spikes = spikes[:MaxDeviceFindingsPerZone]
		}
		out = append(out, spikes...)
	}

	SortFindings(out)
	return out
}

// SortFindings 排序: 严重程度降序, 同级别按置信度降序
func SortFindings(list []Finding) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Severity != list[j].Severity {
			return list[i].Severity > list[j].Severity
		}
		return list[i].Confidence > list[j].Confidence
	})
}

// hasHistory 判断历史里有没有有效数据(用量为正的天)
func hasHistory(hist []DayUsage) bool {
	for _, h := range hist {
		if h.Usage > 0 {
			return true
		}
	}
	return false
}
