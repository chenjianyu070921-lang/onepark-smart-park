package rules

import "testing"

// 造历史数据的辅助函数: 从 startDay 开始连着 n 天, 每天用量都是 v
func hist(n int, v float64) []DayUsage {
	out := make([]DayUsage, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, DayUsage{Date: "2026-09-01", Usage: v})
	}
	return out
}

// ---------- 基线 ----------

func TestBaseline_SkipZeroDays(t *testing.T) {
	// 7 天里有 2 天没数据(0), 基线应该按剩下 5 天算, 而不是除以 7
	h := []DayUsage{
		{Usage: 100}, {Usage: 100}, {Usage: 0}, {Usage: 100}, {Usage: 100}, {Usage: 0}, {Usage: 100},
	}
	avg, days, ok := Baseline(h)
	if !ok {
		t.Fatal("7 天里 5 天有效, 应该算得出基线")
	}
	if days != 5 {
		t.Errorf("有效天数应为 5, 实际 %d", days)
	}
	if avg != 100 {
		t.Errorf("基线应为 100, 实际 %v —— 如果把 0 算进去会变成 71.4, 那样正常日子也会被判异常", avg)
	}
}

func TestBaseline_NotEnoughDays(t *testing.T) {
	// 只有 2 天历史, 不该算基线(基线不足时规则层会跳过判定, 避免冷启动误报)
	_, _, ok := Baseline(hist(2, 100))
	if ok {
		t.Error("只有 2 天历史却算出了基线, 冷启动会批量误报")
	}
	_, _, ok3 := Baseline(hist(3, 100))
	if !ok3 {
		t.Error("3 天有效历史应该够算基线")
	}
}

func TestBaseline_AllZero(t *testing.T) {
	_, days, ok := Baseline(hist(7, 0))
	if ok || days != 0 {
		t.Errorf("全 0 历史不该有基线, days=%d ok=%v", days, ok)
	}
}

// ---------- 区域普涨 ----------

func TestDetectZoneSurge(t *testing.T) {
	th := DefaultThreshold()
	// 基线 100, 当天 180 → 1.8 倍, 超过阈值 1.3, 应该检出
	z := ZoneInput{ZoneID: "A栋", History: hist(7, 100), Today: 180, HasData: true}
	f, ok := DetectZoneSurge(z, th)
	if !ok {
		t.Fatal("1.8 倍于基线却没检出")
	}
	if f.Category != CatZoneSurge || f.ZoneID != "A栋" {
		t.Errorf("分类或区域不对: %+v", f)
	}
	if f.Severity != SeverityMid {
		t.Errorf("1.8 倍应为中级(2), 实际 %d", f.Severity)
	}
	if f.Confidence <= 0.5 || f.Confidence > 0.95 {
		t.Errorf("置信度应在 (0.5, 0.95], 实际 %v", f.Confidence)
	}
	if f.Evidence["baseline"] != 100 || f.Evidence["actual"] != 180 {
		t.Errorf("证据不对: %+v", f.Evidence)
	}
}

func TestDetectZoneSurge_NormalDay(t *testing.T) {
	// 110 度 vs 基线 100 → 1.1 倍, 没超阈值, 不该报(否则运维会被淹)
	z := ZoneInput{ZoneID: "A栋", History: hist(7, 100), Today: 110, HasData: true}
	if _, ok := DetectZoneSurge(z, DefaultThreshold()); ok {
		t.Error("1.1 倍属于正常波动, 不该报")
	}
}

func TestDetectZoneSurge_SkipTinyUsage(t *testing.T) {
	// 基线 0.5 度的小区域, 即使翻倍也没意义, 按 MinUsage 过滤
	z := ZoneInput{ZoneID: "车棚", History: hist(7, 0.5), Today: 2, HasData: true}
	if _, ok := DetectZoneSurge(z, DefaultThreshold()); ok {
		t.Error("用量极小的区域应被 MinUsage 过滤, 否则全是噪音")
	}
}

func TestDetectZoneSurge_BaselineTooShort(t *testing.T) {
	// 只有 2 天历史, 基线不可信, 不判定
	z := ZoneInput{ZoneID: "A栋", History: hist(2, 100), Today: 1000, HasData: true}
	if _, ok := DetectZoneSurge(z, DefaultThreshold()); ok {
		t.Error("基线不足时不该判定, 宁可漏报")
	}
}

// ---------- 夜间空转 ----------

func TestDetectNightIdle_CrossMidnight(t *testing.T) {
	// 关键用例: 夜间跨零点, 23点/0点/1点都要算进夜间
	z := ZoneInput{
		ZoneID: "A栋",
		Today:  100,
		HourToday: []HourUsage{
			{Hour: 23, Usage: 20}, // 夜间
			{Hour: 0, Usage: 15},  // 夜间(跨零点)
			{Hour: 1, Usage: 10},  // 夜间
			{Hour: 9, Usage: 30},  // 白天
			{Hour: 14, Usage: 25}, // 白天
		},
	}
	// 夜间 45 / 全天 100 = 45%, 远超阈值 25%
	f, ok := DetectNightIdle(z, DefaultThreshold())
	if !ok {
		t.Fatal("夜间占 45% 却没检出, 跨零点的小时可能被漏算了")
	}
	if f.Evidence["night"] != 45 {
		t.Errorf("夜间用量应为 45(20+15+10), 实际 %v", f.Evidence["night"])
	}
	if f.Category != CatNightIdle {
		t.Errorf("分类应为 night_idle, 实际 %s", f.Category)
	}
}

func TestDetectNightIdle_Normal(t *testing.T) {
	// 夜间只占 5%, 正常办公园区该有的样子
	z := ZoneInput{
		ZoneID: "A栋",
		Today:  100,
		HourToday: []HourUsage{
			{Hour: 23, Usage: 3}, {Hour: 0, Usage: 2}, {Hour: 9, Usage: 50}, {Hour: 14, Usage: 45},
		},
	}
	if _, ok := DetectNightIdle(z, DefaultThreshold()); ok {
		t.Error("夜间占 5% 是正常的, 不该报")
	}
}

// ---------- 单设备突增 ----------

func TestDetectDeviceSpike(t *testing.T) {
	d := DeviceInput{DeviceID: "METER-A01", ZoneID: "A栋", History: hist(7, 20), Today: 60, HasData: true}
	f, ok := DetectDeviceSpike(d, DefaultThreshold())
	if !ok {
		t.Fatal("3 倍于自身基线却没检出")
	}
	if f.DeviceID != "METER-A01" || f.ZoneID != "A栋" {
		t.Errorf("设备或区域没带上: %+v", f)
	}
	if f.Severity != SeverityHigh {
		t.Errorf("3 倍应为高级(3), 实际 %d", f.Severity)
	}
}

func TestDetectDeviceSpike_CompareWithSelf(t *testing.T) {
	// 一台大功率设备(基线 500)当天 600, 只涨 20%, 不该报。
	// 这验证了"跟自己比而不是跟别人比" —— 600 度看着多, 但对它很正常
	d := DeviceInput{DeviceID: "BIG-01", ZoneID: "A栋", History: hist(7, 500), Today: 600, HasData: true}
	if _, ok := DetectDeviceSpike(d, DefaultThreshold()); ok {
		t.Error("大功率设备涨 20% 属正常, 不该报(说明基线的用法是对的)")
	}
}

// ---------- 数据质量 ----------

func TestDetectMeterBackward(t *testing.T) {
	d := DeviceInput{DeviceID: "METER-B02", ZoneID: "B栋", Backward: true}
	f, ok := DetectMeterBackward(d)
	if !ok {
		t.Fatal("读数回退是硬事实, 必须报")
	}
	if f.Confidence < 0.9 {
		t.Errorf("读数回退置信度应接近 1, 实际 %v", f.Confidence)
	}
}

func TestDetectDataMissing(t *testing.T) {
	// 历史有数据但当天没有 → 缺失
	if _, ok := DetectDataMissing("A栋", "", false, true); !ok {
		t.Error("有历史但当天无数据, 应报缺失")
	}
	// 当天有数据 → 不报
	if _, ok := DetectDataMissing("A栋", "", true, true); ok {
		t.Error("当天有数据不该报缺失")
	}
	// 新装设备(无历史) → 不报, 否则新设备一上线就报警
	if _, ok := DetectDataMissing("新区域", "", false, false); ok {
		t.Error("没有历史的新对象不该报缺失")
	}
}

// ---------- 编排 ----------

func TestAnalyze_TopNPerZone(t *testing.T) {
	// A栋 5 台设备全部突增, 但每个区域最多只报 3 条, 避免刷屏
	devices := []DeviceInput{
		{DeviceID: "D1", ZoneID: "A栋", History: hist(7, 10), Today: 50, HasData: true},
		{DeviceID: "D2", ZoneID: "A栋", History: hist(7, 10), Today: 45, HasData: true},
		{DeviceID: "D3", ZoneID: "A栋", History: hist(7, 10), Today: 40, HasData: true},
		{DeviceID: "D4", ZoneID: "A栋", History: hist(7, 10), Today: 35, HasData: true},
		{DeviceID: "D5", ZoneID: "A栋", History: hist(7, 10), Today: 30, HasData: true},
	}
	in := Input{
		Zones:   []ZoneInput{{ZoneID: "A栋", History: hist(7, 50), Today: 50, HasData: true}},
		Devices: devices,
	}
	got := Analyze(in, DefaultThreshold())

	var spikeCount int
	for _, f := range got {
		if f.Category == CatDeviceSpike {
			spikeCount++
		}
	}
	if spikeCount != MaxDeviceFindingsPerZone {
		t.Errorf("设备级发现应为 %d 条(Top N), 实际 %d", MaxDeviceFindingsPerZone, spikeCount)
	}
	// 留下的应该是偏离最大的那几台(D1/D2/D3)
	for _, f := range got {
		if f.Category == CatDeviceSpike && f.DeviceID == "D5" {
			t.Error("影响最小的 D5 不该出现在结果里, Top N 排序可能有问题")
		}
	}
}

func TestAnalyze_SortedBySeverity(t *testing.T) {
	devices := []DeviceInput{
		{DeviceID: "轻微", ZoneID: "A栋", History: hist(7, 10), Today: 16, HasData: true},  // 1.6 倍, 中级
		{DeviceID: "严重", ZoneID: "A栋", History: hist(7, 10), Today: 50, HasData: true},  // 5 倍, 高级
		{DeviceID: "故障", ZoneID: "A栋", History: hist(7, 10), Today: 10, Backward: true}, // 读数回退
	}
	in := Input{
		Zones:   []ZoneInput{{ZoneID: "A栋", History: hist(7, 100), Today: 100, HasData: true}},
		Devices: devices,
	}
	got := Analyze(in, DefaultThreshold())
	if len(got) < 2 {
		t.Fatalf("至少该有 2 条发现, 实际 %d", len(got))
	}
	// 第一条应该是严重程度最高的
	if got[0].Severity < got[len(got)-1].Severity {
		t.Errorf("结果没按严重程度排序: 第一条 %d, 最后一条 %d", got[0].Severity, got[len(got)-1].Severity)
	}
}

func TestAnalyze_EmptyInput(t *testing.T) {
	// 空输入不该 panic, 返回空列表
	if got := Analyze(Input{}, DefaultThreshold()); len(got) != 0 {
		t.Errorf("空输入应返回空结果, 实际 %d 条", len(got))
	}
}

func TestAnalyze_NoFalsePositiveOnHealthyPark(t *testing.T) {
	// 一个完全正常的园区: 用量稳定、夜间低谷、设备无异常
	// 这类用例是防"狼来了"的底线 —— 正常情况一条都不该报
	in := Input{
		Zones: []ZoneInput{
			{
				ZoneID:  "A栋",
				History: hist(7, 100),
				Today:   102,
				HourToday: []HourUsage{
					{Hour: 23, Usage: 2}, {Hour: 0, Usage: 1},
					{Hour: 9, Usage: 50}, {Hour: 14, Usage: 49},
				},
				HasData: true,
			},
		},
		Devices: []DeviceInput{
			{DeviceID: "M1", ZoneID: "A栋", History: hist(7, 50), Today: 52, HasData: true},
			{DeviceID: "M2", ZoneID: "A栋", History: hist(7, 52), Today: 50, HasData: true},
		},
	}
	got := Analyze(in, DefaultThreshold())
	if len(got) != 0 {
		t.Errorf("健康园区不该有任何发现, 实际报了 %d 条: %+v", len(got), got)
	}
}
