package provider

import "github.com/zeromicro/go-zero/core/logx"

// 本文件集中处理与上游契约的**一致性观测**。
//
// 为什么需要它（而不是让调用方自己盯着）:
// M5 的原则是「上游数字原样透传, 不做本地纠正」—— 真相源在上游, 擅自补齐差额会掩盖上游 bug
// （见 device_test 的 TestDeviceStat_PassThroughOnInconsistency）。
// 但**静默**透传同样危险: 上游新增一个状态码 / 等级码时，大屏上"总数与分组对不上"，
// 却没有任何线索指向谁该修 —— 数据看起来只是"少了一点"。
//
// 所以这里只做一件事: **数据不动, 但把漂移变成可观测的**。
// 差额写进日志，责任与线索都留在正确的位置。

// warnIfDrifted 分项之和与总数不一致时留一条错误日志。
//
// kind 用于区分是哪个统计（"告警"/"设备"）; 差额为 0 时不打日志（正常路径零开销）。
func warnIfDrifted(kind string, total, sum int64) {
	if total == sum {
		return
	}
	logx.Errorf("[provider] %s统计分项之和与总数不一致: total=%d, 分项之和=%d, 差额=%d —— "+
		"上游可能新增了取值而契约未同步, 需双方核对; 数据已按上游原值透传, 未做本地纠正",
		kind, total, sum, total-sum)
}

// warnIfRateOutOfRange 比率类指标超出 [0,1] 时留一条错误日志。
//
// 比率的量纲是「比例」, 越界意味着上游口径变了（例如 completion_rate 从百分比改成了小数,
// 或分母取错）—— 这时大屏会出现"完成率 150%"这种一眼假、但没人知道该找谁的数字。
func warnIfRateOutOfRange(kind string, v float64) {
	if v >= 0 && v <= 1 {
		return
	}
	logx.Errorf("[provider] %s比率超出 [0,1] 区间: %v —— 上游口径可能已变更(如百分比与小数互换),"+
		"需双方核对; 数据已按上游原值透传, 未做本地纠正", kind, v)
}
