// Package rules 是智能体的"层1 规则归因引擎"。
//
// 设计原则(答辩时这几条能讲):
//  1. 纯函数: 不连数据库、不读时钟、不调网络。所有数据由入参传入,
//     所以能写单测, 改坏了立刻知道, 也不受环境影响。
//  2. 确定性: 同样的输入永远得到同样的输出, 不像大模型每次结果可能不同。
//     这意味着它是"可验证"的, 也是大模型挂了之后的兜底。
//  3. 宁漏勿误: 基线不足、用量太小、置信度不够的情况一律不产出建议。
//     误报多了运维就不看这个系统了, 那种"智能"比没有更糟。
//
// 层2(大模型)只负责把这里算出的结论翻译成人话, 不参与任何判定。
package rules

// 异常类型。这五种覆盖了园区能耗最常见的可行动问题,
// 都是"发现之后运维能干点什么"的, 纯统计口径的异常不在此列
const (
	// CatNightIdle 夜间空转: 低水位时段(23:00-06:00)仍有明显用电。
	// 这是肉眼最难发现的一类浪费, 因为白天的用量把夜间的柱子压得很矮
	CatNightIdle = "night_idle"
	// CatDeviceSpike 单设备突增: 某台设备当天用量远高于它自己的历史基线
	CatDeviceSpike = "device_spike"
	// CatZoneSurge 全区域普涨: 整个区域用量高于基线, 通常不是单台设备的锅
	CatZoneSurge = "zone_surge"
	// CatMeterBackward 读数回退: 累计读数出现下降, 疑似表计故障或换表
	CatMeterBackward = "meter_backward"
	// CatDataMissing 数据缺失: 该上报的时间段没有数据, 可能是设备离线
	CatDataMissing = "data_missing"
)

// 严重程度
const (
	SeverityHigh = 3
	SeverityMid  = 2
	SeverityLow  = 1
)

// CategoryName 异常类型的中文名, 展示用
var CategoryName = map[string]string{
	CatNightIdle:     "夜间空转",
	CatDeviceSpike:   "单设备突增",
	CatZoneSurge:     "区域用量普涨",
	CatMeterBackward: "表计读数异常",
	CatDataMissing:   "数据缺失",
}

// DayUsage 某一天某个对象的用量
type DayUsage struct {
	Date  string  `json:"date"` // 2006-01-02
	Usage float64 `json:"usage"`
}

// HourUsage 某小时的用量
type HourUsage struct {
	Hour  int     `json:"hour"` // 0~23
	Usage float64 `json:"usage"`
}

// ZoneInput 一个区域的分析输入
type ZoneInput struct {
	ZoneID    string      // 区域
	History   []DayUsage  // 基线期的每日用量, 不含统计当天
	Today     float64     // 统计当天的用量
	HourToday []HourUsage // 统计当天的分时用量, 夜间判定要用
	HasData   bool        // 当天有没有数据上报
}

// DeviceInput 一台设备的分析输入
type DeviceInput struct {
	DeviceID string
	ZoneID   string
	History  []DayUsage // 基线期的每日用量
	Today    float64    // 统计当天用量
	HasData  bool       // 当天有没有数据
	Backward bool       // 当天有没有出现"累计读数下降"(表计异常)
}

// Finding 一条异常发现。层2 大模型拿它写人话解释, 层3 兜底直接展示它的 Reason
type Finding struct {
	Category   string             // 见上面的五种常量
	ZoneID     string             // 区域
	DeviceID   string             // 相关设备, 区域级发现为空
	Severity   int                // 3高 2中 1低
	Title      string             // 一句话结论
	Reason     string             // 规则版解释(层3 兜底用这个)
	Action     string             // 建议动作
	Confidence float64            // 0~1
	Evidence   map[string]float64 // 证据: baseline/actual/ratio 等, 展示给人看
}

// Threshold 判定阈值。rules 包自己定义, 不依赖 config,
// 这样单测里能随便构造阈值, 也不用引框架
type Threshold struct {
	SpikeRatio    float64 // 单设备超过自身基线几倍算突增
	ZoneRatio     float64 // 区域超过基线几倍算普涨
	NightRatio    float64 // 夜间用量占全天超过多少算空转
	MinUsage      float64 // 全天用量低于此值的对象不分析, 过滤噪音
	MinConfidence float64 // 置信度低于此值不产出
}

// DefaultThreshold 默认阈值, 偏保守
func DefaultThreshold() Threshold {
	return Threshold{
		SpikeRatio:    1.5,
		ZoneRatio:     1.3,
		NightRatio:    0.25,
		MinUsage:      1.0,
		MinConfidence: 0.5,
	}
}
