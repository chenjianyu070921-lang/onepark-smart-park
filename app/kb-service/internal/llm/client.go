// Package llm 大模型客户端(可选件).
//
// 与 M4 智能体服务的 llm 包同一套约定, 这是刻意保持的一致性:
//   - 抽成接口: 测试注入 mock(不依赖网络/key), 换厂商只换实现;
//   - OpenAI 兼容协议: DeepSeek / 火山方舟 / 千问 / 智谱 / Kimi 都走 /chat/completions;
//   - **失败即降级**: 这里返回 error, 由上层回退到抽取式回答 —— 大模型是增强项, 不是单点。
//
// 与 M4 的关系: 代码是同构的, 但没有跨模块 import(各模块的 llm 包各自持有)。
// 后续若要收敛成全仓一份, 建议提到 common/llm 并与 M4 负责人对齐 —— 这条已记在融合说明里,
// 本次答辩前不动别人的模块。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 大模型客户端.
type Client interface {
	// Generate 按 system + user 两段提示生成回答, 返回 (文本, 消耗 token, 错误).
	Generate(ctx context.Context, system, user string) (string, int, error)
	// Name 实现名, 记进问答日志便于事后归因(openai / mock)
	Name() string
}

// Options 构造参数.
type Options struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// New 按配置返回客户端: 未启用返回 nil(调用方据此走抽取式)。
func New(enable bool, opt Options) Client {
	if !enable || strings.TrimSpace(opt.APIKey) == "" || strings.TrimSpace(opt.BaseURL) == "" {
		return nil
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Second
	}
	opt.BaseURL = strings.TrimRight(opt.BaseURL, "/")
	return &openAIClient{opt: opt, hc: &http.Client{Timeout: opt.Timeout}}
}

// ---------- OpenAI 兼容实现 ----------

type openAIClient struct {
	opt Options
	hc  *http.Client
}

func (c *openAIClient) Name() string { return "openai:" + c.opt.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openAIClient) Generate(ctx context.Context, system, user string) (string, int, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.opt.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		// 知识库问答要的是"照着材料说", 不是创作 -> 温度压低
		Temperature: 0.2,
		MaxTokens:   800,
	})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opt.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.opt.APIKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", 0, fmt.Errorf("大模型返回无法解析(status=%d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || parsed.Error != nil {
		msg := fmt.Sprintf("status=%d", resp.StatusCode)
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return "", 0, fmt.Errorf("大模型调用失败: %s", msg)
	}
	if len(parsed.Choices) == 0 {
		return "", 0, fmt.Errorf("大模型返回空 choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), parsed.Usage.TotalTokens, nil
}

// ---------- Mock 实现(测试 / 离线演示) ----------

type mockClient struct{ answer string }

// NewMock 返回固定回答的客户端, 只用于测试与离线演示。
func NewMock(answer string) Client { return &mockClient{answer: answer} }

func (c *mockClient) Name() string { return "mock" }

func (c *mockClient) Generate(context.Context, string, string) (string, int, error) {
	return c.answer, 0, nil
}
