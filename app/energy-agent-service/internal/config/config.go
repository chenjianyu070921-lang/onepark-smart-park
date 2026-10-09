package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf

	// MySQL 组内公用库 onepark-smart-park
	// 这里要读 energy_reading(用量) 和 device(设备台账), 写 agent_* 三张表
	MySQL struct {
		DataSource string
	}

	// LLM 大模型配置(层2, 只负责把规则算出的结论翻译成人话)
	// 用的是 OpenAI 兼容接口, DeepSeek / 通义 / 智谱 / Kimi / OpenAI 都能填
	// Enable 关掉或调用失败时, 系统自动降级为规则原文, 不影响出报告
	LLM struct {
		Enable  bool   // false 则全程走 mock 兜底
		BaseURL string // 如 https://api.deepseek.com/v1
		APIKey  string // 建议用环境变量覆盖, 别提交到 git
		Model   string // 如 deepseek-chat
		Timeout int    // 单次调用超时(秒)
	}

	// InspectJob 定时巡检
	InspectJob struct {
		Enable bool
		Hour   int // 每天几点跑(24小时制), 服务启动时会先跑一次
	}

	// 判定阈值。yaml 里不写就用代码里的默认值
	// 调参方向是"宁漏勿误": 误报多了运维就不再看这个系统了
	Threshold ThresholdConf
}

// ThresholdConf 判定阈值, 单独定义成类型是为了能在函数签名里用
type ThresholdConf struct {
	BaselineDays  float64 // 基线取最近几天, 默认 7
	SpikeRatio    float64 // 单设备超过自身基线几倍算突增, 默认 1.5
	ZoneRatio     float64 // 区域超过基线几倍算普涨, 默认 1.3
	NightRatio    float64 // 夜间用量占全天超过多少算空转, 默认 0.25
	MinUsage      float64 // 全天用量低于几度的区域不分析, 默认 1, 过滤噪音
	MinConfidence float64 // 置信度低于此值不产出建议, 默认 0.5
}

// 夜间时段 [23:00, 06:00), 跨零点, 这段时间的用量单独统计用来发现空转
const (
	NightStartHour = 23
	NightEndHour   = 6
)

// 默认阈值, yaml 没配或配了 0 时用这些
const (
	DefBaselineDays  = 7.0
	DefSpikeRatio    = 1.5
	DefZoneRatio     = 1.3
	DefNightRatio    = 0.25
	DefMinUsage      = 1.0
	DefMinConfidence = 0.5
)

// Thresholds 返回填好默认值的阈值配置, yaml 没配的项自动补默认值
func (c Config) Thresholds() ThresholdConf {
	t := c.Threshold
	if t.BaselineDays <= 0 {
		t.BaselineDays = DefBaselineDays
	}
	if t.SpikeRatio <= 0 {
		t.SpikeRatio = DefSpikeRatio
	}
	if t.ZoneRatio <= 0 {
		t.ZoneRatio = DefZoneRatio
	}
	if t.NightRatio <= 0 {
		t.NightRatio = DefNightRatio
	}
	if t.MinUsage < 0 {
		t.MinUsage = DefMinUsage
	}
	if t.MinConfidence <= 0 {
		t.MinConfidence = DefMinConfidence
	}
	return t
}
