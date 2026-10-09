package rules

import "fmt"

// 五类异常的检测函数。每个都是纯函数: 吃输入吐结果, 不碰数据库和网络。
// 返回值第二个是"有没有检出", false 表示一切正常或数据不足以判定。

// DetectZoneSurge 区域用量普涨: 当天用量明显高于该区域自己的历史基线
func DetectZoneSurge(z ZoneInput, t Threshold) (Finding, bool) {
	base, days, ok := Baseline(z.History)
	if !ok {
		// 历史数据不够, 不判定。宁可不报也不能乱报
		return Finding{}, false
	}
	// 当天用量和基线都得过门槛。只卡当天用量是不够的:
	// 车棚这种基线 0.5 度的地方, 翻到 2 度就是 4 倍, 报出来纯属噪音
	if z.Today < t.MinUsage || base < t.MinUsage {
		return Finding{}, false
	}
	ratio := Ratio(z.Today, base)
	if ratio < t.ZoneRatio {
		return Finding{}, false
	}
	conf := ConfidenceByRatio(ratio)
	if conf < t.MinConfidence {
		return Finding{}, false
	}

	f := Finding{
		Category:   CatZoneSurge,
		ZoneID:     z.ZoneID,
		Severity:   SeverityByRatio(ratio),
		Title:      fmt.Sprintf("%s 当天用量 %.0f 度, 高于基线 %.0f%%", z.ZoneID, z.Today, (ratio-1)*100),
		Reason:     fmt.Sprintf("该区域近 %d 个有效日的日均用量为 %.2f 度, 当天实际 %.2f 度, 高出 %.0f%%, 多用了约 %.2f 度。", days, base, z.Today, (ratio-1)*100, z.Today-base),
		Action:     "排查该区域是否有新增负载、设备长时间运行或空调温度设置过低",
		Confidence: conf,
		Evidence: map[string]float64{
			"baseline":  Round2(base),
			"actual":    Round2(z.Today),
			"ratio":     Round2(ratio),
			"delta":     Round2(z.Today - base),
			"days":      float64(days),
			"deviation": Round2((ratio - 1) * 100), // 偏离百分比
		},
	}
	return f, true
}

// DetectNightIdle 夜间空转: 23 点到次日 6 点的用量占全天比例过高。
// 这是整套规则里最有价值的一条 —— 白天的柱子比夜间高得多,
// 光看柱状图基本注意不到夜间那一段, 但它往往就是白白烧掉的钱。
func DetectNightIdle(z ZoneInput, t Threshold) (Finding, bool) {
	if len(z.HourToday) == 0 {
		return Finding{}, false
	}
	night, total := NightUsage(z.HourToday)
	if total < t.MinUsage {
		return Finding{}, false
	}
	share := night / total
	if share < t.NightRatio {
		return Finding{}, false
	}
	conf := ConfidenceByExcess(share, t.NightRatio)
	if conf < t.MinConfidence {
		return Finding{}, false
	}

	f := Finding{
		Category:   CatNightIdle,
		ZoneID:     z.ZoneID,
		Severity:   severityByNightShare(share),
		Title:      fmt.Sprintf("%s 夜间用电占全天 %.0f%%, 疑似空转", z.ZoneID, share*100),
		Reason:     fmt.Sprintf("当天总用量 %.2f 度, 其中夜间(23:00-次日06:00)用了 %.2f 度, 占 %.0f%%。深夜通常无人办公, 这个占比偏高, 可能存在设备未关闭。", total, night, share*100),
		Action:     "核查夜间未关闭的照明、空调、饮水机与机房设备, 必要时设置定时断电",
		Confidence: conf,
		Evidence: map[string]float64{
			"night":   Round2(night),
			"total":   Round2(total),
			"share":   Round2(share * 100), // 夜间占全天百分比
			"hours":   7,                   // 夜间共 7 个小时
			"average": Round2(night / 7),   // 夜间平均每小时用量
			// deviation 统一表示"偏离幅度", 供大模型和兜底文案用。
			// 夜间这条没有基线可比, 就用占比当偏离幅度
			"deviation": Round2(share * 100),
		},
	}
	return f, true
}

// DetectDeviceSpike 单设备突增: 拿设备跟它自己的历史比, 而不是跟别的设备比。
// 不同设备功率差几倍很正常, 只有跟自己比才有意义。
func DetectDeviceSpike(d DeviceInput, t Threshold) (Finding, bool) {
	base, days, ok := Baseline(d.History)
	if !ok {
		return Finding{}, false
	}
	// 同区域判定: 当天用量和自身基线都得过门槛
	if d.Today < t.MinUsage || base < t.MinUsage {
		return Finding{}, false
	}
	ratio := Ratio(d.Today, base)
	if ratio < t.SpikeRatio {
		return Finding{}, false
	}
	conf := ConfidenceByRatio(ratio)
	if conf < t.MinConfidence {
		return Finding{}, false
	}

	f := Finding{
		Category:   CatDeviceSpike,
		ZoneID:     d.ZoneID,
		DeviceID:   d.DeviceID,
		Severity:   SeverityByRatio(ratio),
		Title:      fmt.Sprintf("设备 %s 当天用量 %.0f 度, 达自身基线 %.1f 倍", d.DeviceID, d.Today, ratio),
		Reason:     fmt.Sprintf("该设备近 %d 个有效日的日均用量为 %.2f 度, 当天实际 %.2f 度, 是基线的 %.2f 倍, 多用了约 %.2f 度。", days, base, d.Today, ratio, d.Today-base),
		Action:     "现场核查该设备运行状态, 重点关注是否持续满载、散热异常或控制回路失效",
		Confidence: conf,
		Evidence: map[string]float64{
			"baseline":  Round2(base),
			"actual":    Round2(d.Today),
			"ratio":     Round2(ratio),
			"delta":     Round2(d.Today - base),
			"days":      float64(days),
			"deviation": Round2((ratio - 1) * 100),
		},
	}
	return f, true
}

// DetectMeterBackward 表计读数回退: 累计读数只增不减, 出现下降说明表计或采集有问题。
// 这类不算"浪费", 但数据不可信会让上面所有分析都失真, 所以必须报。
func DetectMeterBackward(d DeviceInput) (Finding, bool) {
	if !d.Backward {
		return Finding{}, false
	}
	f := Finding{
		Category:   CatMeterBackward,
		ZoneID:     d.ZoneID,
		DeviceID:   d.DeviceID,
		Severity:   SeverityMid,
		Title:      fmt.Sprintf("设备 %s 出现读数回退, 数据可能不可信", d.DeviceID),
		Reason:     "电表累计读数正常情况下只增不减, 当天检测到读数下降, 可能是表计故障、更换表计或采集异常。在修复前, 该设备的用量统计会偏小。",
		Action:     "联系运维现场核对该表计, 确认是否换表或故障, 修复后补录正确读数",
		Confidence: 0.9, // 读数回退是硬事实, 置信度给高
		Evidence:   map[string]float64{},
	}
	return f, true
}

// DetectDataMissing 数据缺失: 历史一直在上报, 当天却一条都没有。
// 可能是设备离线、网络故障、采集服务挂了。
func DetectDataMissing(zoneID, deviceID string, hasToday, hadHistory bool) (Finding, bool) {
	if hasToday || !hadHistory {
		// 今天有数据, 或者本来就没历史(新装的设备), 都不算缺失
		return Finding{}, false
	}
	name := zoneID
	if deviceID != "" {
		name = "设备 " + deviceID
	}
	f := Finding{
		Category:   CatDataMissing,
		ZoneID:     zoneID,
		DeviceID:   deviceID,
		Severity:   SeverityMid,
		Title:      fmt.Sprintf("%s 当天无数据上报", name),
		Reason:     "该对象过去一直正常上报数据, 但统计当天没有任何记录。可能是设备离线、网络中断或采集服务异常, 需要确认是否影响计费与监控。",
		Action:     "检查设备供电与网络连接, 确认采集服务运行状态",
		Confidence: 0.85,
		Evidence:   map[string]float64{},
	}
	return f, true
}

// severityByNightShare 按夜间占比定严重程度。
// 夜间占一半以上说明基本是 24 小时不间断在跑, 属于高优先级
func severityByNightShare(share float64) int {
	switch {
	case share >= 0.5:
		return SeverityHigh
	case share >= 0.35:
		return SeverityMid
	default:
		return SeverityLow
	}
}
