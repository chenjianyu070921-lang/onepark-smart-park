package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAI 起一个假的 OpenAI 兼容服务, 用来验证真模型那条链路。
//
// 为什么要有这个测试: 没配 key 的时候跑的都是 mock, OpenAIClient 里的代码
// (拼请求、加 Authorization 头、解析 choices、处理各家不一样的报错格式)
// 一行都没被执行过。等真接上 DeepSeek 才发现请求格式写错就晚了 ——
// 而且那时候的表现是"每条建议都静默降级成规则原文", 看起来系统正常运行,
// 实际上大模型根本没生效, 这种问题在答辩现场最难查。
func fakeOpenAI(t *testing.T, handler http.HandlerFunc) (*OpenAIClient, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c := &OpenAIClient{
		baseURL: srv.URL,
		apiKey:  "sk-test",
		model:   "fake-model",
		timeout: 0, // 走默认 20 秒
	}
	return c, srv.Close
}

func TestOpenAIClient_Success(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	c, closeFn := fakeOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "A栋今天比平时多用了不少电, 建议先看看空调。"}},
			},
			"usage": map[string]int{"total_tokens": 123},
		})
	})
	defer closeFn()

	text, tokens, err := c.Explain(context.Background(), ExplainRequest{ZoneID: "A栋", Category: "zone_surge"})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if text != "A栋今天比平时多用了不少电, 建议先看看空调。" {
		t.Errorf("解释文本不对: %s", text)
	}
	if tokens != 123 {
		t.Errorf("token 数没解析出来: %d", tokens)
	}

	// 请求格式必须对: 路径、鉴权头、模型名、两条消息
	if gotPath != "/chat/completions" {
		t.Errorf("请求路径错了: %s (有些厂商地址要带 /v1, 这里只拼接, 地址写错就会 404)", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization 头不对: %s", gotAuth)
	}
	if !strings.Contains(gotBody, `"model":"fake-model"`) {
		t.Errorf("请求体里没有模型名: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"role":"system"`) || !strings.Contains(gotBody, `"role":"user"`) {
		t.Errorf("请求体里缺少 system/user 消息: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"temperature":0`) {
		t.Errorf("温度应该是 0, 保证同样输入同样输出: %s", gotBody)
	}
}

func TestBuildPrompt_NoFakeZeros(t *testing.T) {
	// 读数回退、数据缺失这类发现没有用量和基线, 不该往提示词里塞 "0.00 度"。
	// 给了 0 模型就会照着 0 度编话, 比不给更糟。
	_, user := BuildPrompt(ExplainRequest{
		ZoneID: "A栋", DeviceID: "M1", Category: "meter_backward", StatDate: "2026-09-16",
	})
	if strings.Contains(user, "当天用量") || strings.Contains(user, "历史基线") || strings.Contains(user, "偏离幅度") {
		t.Errorf("没有数字时不该出现用量/基线/偏离行: %s", user)
	}
	if !strings.Contains(user, "表计读数异常") {
		t.Errorf("异常类型还是要有的: %s", user)
	}
}

func TestOpenAIClient_HTTPError(t *testing.T) {
	// 状态码非 200(key 填错、余额不足、模型名写错都会走到这里)
	c, closeFn := fakeOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	})
	defer closeFn()

	_, _, err := c.Explain(context.Background(), ExplainRequest{})
	if err == nil {
		t.Fatal("401 应该返回错误, 而不是当成成功")
	}
	// 错误信息里要带上厂商给的原文, 否则排查 key 问题会很慢
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("错误信息里没带上厂商原文: %v", err)
	}
}

func TestOpenAIClient_BodyError(t *testing.T) {
	// 有的厂商 HTTP 200 但把错误塞在 body 的 error 字段里
	c, closeFn := fakeOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"message":"model not found"}}`))
	})
	defer closeFn()

	if _, _, err := c.Explain(context.Background(), ExplainRequest{}); err == nil {
		t.Fatal("body 里的 error 字段不该被忽略")
	}
}

func TestOpenAIClient_EmptyChoices(t *testing.T) {
	c, closeFn := fakeOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	})
	defer closeFn()

	if _, _, err := c.Explain(context.Background(), ExplainRequest{}); err == nil {
		t.Fatal("choices 为空应该报错, 让上层走兜底")
	}
}
