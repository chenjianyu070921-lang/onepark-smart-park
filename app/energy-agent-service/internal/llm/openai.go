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

// OpenAIClient 手写实现的 OpenAI 兼容客户端。
//
// 为什么不用官方 SDK: 这个服务只需要"发一段话收一段话"这一个能力,
// 引一个 SDK 会带进来一堆用不上的依赖。而 OpenAI 的聊天接口格式
// 已经被国内外几乎所有厂商兼容(DeepSeek/通义/智谱/Kimi/月之暗面),
// 手写这几十行就能通吃, 换厂商只改配置里的地址和模型名。
type OpenAIClient struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
}

// Name 返回模型名
func (c *OpenAIClient) Name() string { return c.model }

// chatRequest 请求体, 只带用得上的字段
type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	// 各家报错格式不统一, 有的放在 error 字段里, 有的用 HTTP 状态码
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Explain 调用大模型生成解释
func (c *OpenAIClient) Explain(ctx context.Context, req ExplainRequest) (string, int, error) {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	system, user := BuildPrompt(req)
	payload := chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		// 温度给 0: 要的是稳定复述, 不是创意写作, 同样输入应该得到同样输出
		Temperature: 0,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, fmt.Errorf("序列化请求失败: %w", err)
	}

	url := strings.TrimSuffix(c.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("调用大模型失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 把响应体截一段放进错误信息, 排查 key 填错还是模型名错了会快很多
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return "", 0, fmt.Errorf("大模型返回状态码 %d: %s", resp.StatusCode, snippet)
	}

	var out chatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", 0, fmt.Errorf("解析响应失败: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", 0, fmt.Errorf("大模型报错: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", 0, fmt.Errorf("大模型没有返回任何内容")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), out.Usage.TotalTokens, nil
}
