package llm

import (
	"context"
	"fmt"
)

// MockClient 兜底实现。没有 key、关掉大模型、或者调用失败时都用它。
//
// 它不是随便返回一句"服务不可用", 而是用规则算出的数字拼出一句通顺的人话。
// 这样即使全程不开大模型, 演示出来的效果依然是完整的 ——
// 少了些润色, 但结论、数字、建议一个不少。这正是"层3 兜底"的意义。
type MockClient struct{}

// Name 返回 mock, 记到运行记录里一眼能看出这次没走大模型
func (m *MockClient) Name() string { return "mock" }

// Explain 用模板拼一句人话, 不联网、不消耗 token
func (m *MockClient) Explain(_ context.Context, req ExplainRequest) (string, int, error) {
	return mockText(req), 0, nil
}

// mockText 按异常类型拼话术。数字全部来自入参, 不编造
func mockText(req ExplainRequest) string {
	target := req.ZoneID
	if req.DeviceID != "" {
		target = fmt.Sprintf("%s的%s", req.ZoneID, req.DeviceID)
	}

	switch req.Category {
	case "night_idle":
		return fmt.Sprintf("%s当天夜间(23点至次日6点)用电占全天 %.0f%%, 深夜无人时段这个占比偏高, 建议核查是否有照明、空调或机房设备未关闭。",
			target, req.Deviation)
	case "device_spike":
		return fmt.Sprintf("%s当天用量 %.0f 度, 是自身历史基线 %.0f 度的 %.1f 倍, 建议现场核查该设备是否持续满载或控制回路异常。",
			target, req.Usage, req.Baseline, ratioOf(req.Usage, req.Baseline))
	case "zone_surge":
		return fmt.Sprintf("%s当天用量 %.0f 度, 比基线 %.0f 度高出 %.0f%%, 建议优先排查是否有新增负载或设备长时间运行。",
			target, req.Usage, req.Baseline, req.Deviation)
	case "meter_backward":
		return fmt.Sprintf("%s出现累计读数下降, 正常情况下电表读数只增不减, 疑似表计故障或换表, 建议现场核对。", target)
	case "data_missing":
		return fmt.Sprintf("%s当天没有任何数据上报, 但此前一直正常, 建议检查设备供电、网络连接与采集服务状态。", target)
	default:
		return fmt.Sprintf("%s当天用量 %.0f 度, 与基线 %.0f 度相比偏离 %.0f%%, 建议关注。",
			target, req.Usage, req.Baseline, req.Deviation)
	}
}

func ratioOf(usage, baseline float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return usage / baseline
}
