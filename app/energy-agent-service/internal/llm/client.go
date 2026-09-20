// Package llm 是智能体的"层2 解释层": 把层1 规则算出的结论翻译成人话。
//
// 它不参与任何异常判定 —— 判定的活全在 rules 包, 那里是确定性的、可单测的。
// 大模型只负责表达, 所以:
//   - 它挂了、超时了、key 失效了、断网了, 系统只是少了段润色, 报告照常出(层3 兜底)
//   - 它胡说八道也影响不了数字, 因为数字是规则算的, 它只是复述
//
// 这个边界是整套设计最要紧的地方, 答辩时值得专门讲一句:
// "让确定性的归确定性, 让概率性的只做表达。"
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExplainRequest 一次解释请求。字段都是层1 已经算好的结构化结论
type ExplainRequest struct {
	ZoneID    string  // 区域
	DeviceID  string  // 设备, 区域级发现为空
	Category  string  // 异常类型, 见 rules 包的常量
	StatDate  string  // 统计日期
	Usage     float64 // 当天用量
	Baseline  float64 // 基线
	Deviation float64 // 偏离百分比
}

// Client 大模型客户端。抽成接口有两个用处:
//  1. 测试时可以注入 mock, 不依赖网络和 key
//  2. 换厂商时只换实现, 上层代码不动
type Client interface {
	// Explain 把结构化结论翻译成一句人话。
	// 返回 (解释文本, 消耗的 token 数, 错误)。
	// 出错时调用方应当回退到规则原文, 而不是让整个巡检失败。
	Explain(ctx context.Context, req ExplainRequest) (string, int, error)

	// Name 返回实现名, 记到 agent_run 表里, 事后能看出这次用的什么
	Name() string
}

// Config 大模型配置
type Config struct {
	Enable  bool   // 关闭时直接用 mock 实现
	BaseURL string // OpenAI 兼容地址
	APIKey  string
	Model   string
	Timeout int // 超时秒数, 小于 1 按 20 秒算
}

// NewClient 按配置造客户端。
// 没启用、没填 key、没填地址时一律返回 mock —— 这是"永远可用"的保证,
// 也意味着这个服务在没有 key 的环境里照样能跑完整流程。
func NewClient(c Config) Client {
	if !c.Enable || c.APIKey == "" || c.BaseURL == "" {
		return &MockClient{}
	}
	return &OpenAIClient{
		baseURL: c.BaseURL,
		apiKey:  c.APIKey,
		model:   c.Model,
		timeout: time.Duration(c.Timeout) * time.Second,
	}
}

// ExplainWithFallback 带兜底的解释。
// 大模型不可用时返回 fallback(规则原文), 并把错误返回给调用方记日志,
// 但绝不因为大模型的问题让整次巡检失败。
func ExplainWithFallback(ctx context.Context, c Client, req ExplainRequest, fallback string) (text string, tokens int, err error) {
	if c == nil {
		return fallback, 0, nil
	}
	text, tokens, err = c.Explain(ctx, req)
	if err != nil {
		return fallback, 0, err
	}
	// 有些模型会返回空串或纯空白, 这种"成功但没内容"也要兜底
	if strings.TrimSpace(text) == "" {
		return fallback, tokens, nil
	}
	return strings.TrimSpace(text), tokens, nil
}

// BuildPrompt 构造给大模型的提示词。
// 单独拎出来是为了能写单测: 提示词改坏了(比如漏了关键数字)测试会红。
func BuildPrompt(req ExplainRequest) (system, user string) {
	system = "你是一个园区能源管理助手。你会拿到一段系统自动检测的结论, " +
		"请用一到两句中文向运维人员解释清楚这件事, 语气平实, 不要夸张, 不要使用感叹号。\n" +
		"严格要求:\n" +
		"1. 只能使用我给你的数字, 禁止自己推算或编造新的数字\n" +
		"2. 不要重复我已经给出的结论原文, 换一种更口语的说法\n" +
		"3. 给出一条具体可执行的下一步动作\n" +
		"4. 全部用中文, 不超过 80 字"

	target := req.ZoneID
	if req.DeviceID != "" {
		target = fmt.Sprintf("%s 的设备 %s", req.ZoneID, req.DeviceID)
	}

	user = fmt.Sprintf("检测日期: %s\n对象: %s\n异常类型: %s",
		req.StatDate, target, categoryName(req.Category))
	// 只有真有数字的时候才往提示词里塞。
	// 读数回退、数据缺失这类发现本来就没有用量和基线, 硬塞两个 0.00 进去,
	// 模型要么照着 0 度编一段话, 要么干脆不敢提数字 —— 两种都不如不给。
	if req.Usage > 0 || req.Baseline > 0 {
		user += fmt.Sprintf("\n当天用量: %.2f 度\n历史基线: %.2f 度", req.Usage, req.Baseline)
	}
	if req.Deviation != 0 {
		user += fmt.Sprintf("\n偏离幅度: %.0f%%", req.Deviation)
	}
	return system, user
}

// categoryName 异常类型中文名, 避免 llm 包依赖 rules 包
func categoryName(cat string) string {
	m := map[string]string{
		"night_idle":     "夜间空转",
		"device_spike":   "单设备突增",
		"zone_surge":     "区域用量普涨",
		"meter_backward": "表计读数异常",
		"data_missing":   "数据缺失",
	}
	if v, ok := m[cat]; ok {
		return v
	}
	return cat
}
