package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewClient_FallbackToMock(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"未启用", Config{Enable: false, APIKey: "sk-x", BaseURL: "http://a"}},
		{"没填key", Config{Enable: true, APIKey: "", BaseURL: "http://a"}},
		{"没填地址", Config{Enable: true, APIKey: "sk-x", BaseURL: ""}},
	}
	// 这三种情况都必须返回 mock, 保证服务在任何环境都能跑完整流程
	for _, c := range cases {
		got := NewClient(c.cfg)
		if got.Name() != "mock" {
			t.Errorf("%s 时应回退到 mock, 实际 %s", c.name, got.Name())
		}
	}

	// 配置齐全才用真模型
	real := NewClient(Config{Enable: true, APIKey: "sk-x", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"})
	if real.Name() != "deepseek-chat" {
		t.Errorf("配置齐全应用真模型, 实际 %s", real.Name())
	}
}

func TestBuildPrompt_ContainsKeyNumbers(t *testing.T) {
	req := ExplainRequest{
		ZoneID:    "A栋",
		DeviceID:  "METER-A01",
		Category:  "device_spike",
		StatDate:  "2026-09-16",
		Usage:     180,
		Baseline:  100,
		Deviation: 80,
	}
	system, user := BuildPrompt(req)

	// 数字必须完整出现在提示词里, 否则大模型只能瞎编
	for _, want := range []string{"180", "100", "80", "A栋", "METER-A01", "2026-09-16"} {
		if !strings.Contains(user, want) {
			t.Errorf("提示词里缺关键信息 %q: %s", want, user)
		}
	}
	// 系统提示要约束住不许编数字
	if !strings.Contains(system, "禁止") {
		t.Error("系统提示词没有约束大模型编造数字, 这条约束很重要")
	}
	// 异常类型应翻译成中文, 直接给英文会影响理解
	if !strings.Contains(user, "单设备突增") {
		t.Errorf("异常类型没翻译成中文: %s", user)
	}
}

func TestMockClient_AllCategories(t *testing.T) {
	// 每种异常类型都要能拼出话来, 不能出现空字符串
	cats := []string{"night_idle", "device_spike", "zone_surge", "meter_backward", "data_missing", "unknown_type"}
	for _, cat := range cats {
		req := ExplainRequest{ZoneID: "A栋", DeviceID: "M1", Category: cat, Usage: 100, Baseline: 50, Deviation: 100}
		text, tokens, err := (&MockClient{}).Explain(context.Background(), req)
		if err != nil {
			t.Errorf("%s 不该返回错误: %v", cat, err)
		}
		if text == "" {
			t.Errorf("%s 的解释是空的", cat)
		}
		if tokens != 0 {
			t.Errorf("mock 不消耗 token, 实际 %d", tokens)
		}
	}
}

// errClient 一个必定失败的客户端, 用来验证兜底逻辑
type errClient struct{}

func (errClient) Explain(context.Context, ExplainRequest) (string, int, error) {
	return "", 0, errors.New("模拟: 大模型超时")
}
func (errClient) Name() string { return "err" }

func TestExplainWithFallback_ModelDown(t *testing.T) {
	// 大模型挂了, 应该返回规则原文, 且把错误交出去记日志, 但绝不中断巡检
	fallback := "规则原文: A栋用量偏高一倍"
	text, _, err := ExplainWithFallback(context.Background(), errClient{}, ExplainRequest{}, fallback)
	if err == nil {
		t.Error("大模型失败时应该把错误返回给调用方记日志")
	}
	if text != fallback {
		t.Errorf("大模型挂了应回退到规则原文, 实际: %s", text)
	}
}

func TestExplainWithFallback_EmptyResponse(t *testing.T) {
	// 模型返回了空字符串(有些模型会这样), 也要回退
	text, _, _ := ExplainWithFallback(context.Background(), emptyClient{}, ExplainRequest{}, "兜底文本")
	if text != "兜底文本" {
		t.Errorf("空响应应回退, 实际: %s", text)
	}
}

type emptyClient struct{}

func (emptyClient) Explain(context.Context, ExplainRequest) (string, int, error) {
	return "   ", 0, nil
}
func (emptyClient) Name() string { return "empty" }

func TestExplainWithFallback_NilClient(t *testing.T) {
	// 客户端是 nil 也不该 panic
	text, _, err := ExplainWithFallback(context.Background(), nil, ExplainRequest{}, "兜底")
	if err != nil || text != "兜底" {
		t.Errorf("nil 客户端应安全返回兜底文本, 实际 text=%q err=%v", text, err)
	}
}
