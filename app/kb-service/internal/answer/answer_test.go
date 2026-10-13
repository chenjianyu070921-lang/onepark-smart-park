package answer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"onepark/app/kb-service/internal/llm"
	"onepark/app/kb-service/internal/retriever"
)

type errClient struct{}

func (errClient) Name() string { return "err" }
func (errClient) Generate(context.Context, string, string) (string, int, error) {
	return "", 0, errors.New("模拟大模型超时")
}

func hits() []retriever.Hit {
	return []retriever.Hit{
		{Score: 0.82, Chunk: retriever.Chunk{
			ID: 1, DocumentID: 1, Seq: 0, Title: "调度策略设计", Source: "docs/m5/03-调度策略设计.md",
			Content: "工单超时没人接时，超时重派会把工单释放回待指派状态。释放时保留审计流水，处理人能看到前一次派给了谁。",
		}},
		{Score: 0.55, Chunk: retriever.Chunk{
			ID: 2, DocumentID: 1, Seq: 1, Title: "调度策略设计", Source: "docs/m5/03-调度策略设计.md",
			Content: "智能派单按园区、技能、负载三项打分，选得分最高的人。",
		}},
	}
}

func TestBuild_无命中则拒答(t *testing.T) {
	got := New(nil).Build(context.Background(), "食堂今天吃什么", nil)
	if got.Mode != ModeNoHit {
		t.Fatalf("无命中应是 no_hit, 实际 %s", got.Mode)
	}
	if !strings.Contains(got.Text, "没有找到") {
		t.Errorf("拒答文案应明确说明没有依据, 实际: %s", got.Text)
	}
	if strings.Contains(got.Text, "http") {
		t.Error("拒答文案不应编造链接/来源")
	}
}

func TestBuild_抽取式含出处(t *testing.T) {
	got := New(nil).Build(context.Background(), "工单超时没人接怎么处理", hits())
	if got.Mode != ModeExtractive {
		t.Fatalf("无大模型时应走抽取式, 实际 %s", got.Mode)
	}
	for _, want := range []string{"【结论】", "【依据】", "调度策略设计", "第 0 段", "docs/m5/03-调度策略设计.md"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("抽取式回答缺少 %q\n%s", want, got.Text)
		}
	}
	if !strings.Contains(got.Text, "超时重派") {
		t.Error("结论应包含与提问最相关的原句")
	}
}

func TestBuild_大模型失败回退抽取式(t *testing.T) {
	got := New(errClient{}).Build(context.Background(), "工单超时没人接怎么处理", hits())
	if got.Mode != ModeExtractive {
		t.Fatalf("大模型失败应回退抽取式, 实际 %s", got.Mode)
	}
	if !strings.Contains(got.Text, "【依据】") {
		t.Error("回退后仍应有出处")
	}
}

func TestBuild_大模型成功走生成式(t *testing.T) {
	got := New(llm.NewMock("依据材料：超时重派把工单释放回待指派 [1]。")).Build(context.Background(), "工单超时没人接怎么处理", hits())
	if got.Mode != ModeLLM {
		t.Fatalf("大模型可用应走 llm 模式, 实际 %s", got.Mode)
	}
	if !strings.Contains(got.Text, "[1]") {
		t.Errorf("生成式回答应带来源标注, 实际: %s", got.Text)
	}
}

func TestBuild_命中但抽不到含实词的句子则拒答(t *testing.T) {
	// 检索命中了一个切片, 但里面挑不出含实词的完整句 -> 与"没检索到"同等待遇: 拒答。
	// (第一版这里会兜底吐一句原文, 结果是"看起来答了、其实答非所问", 已按第二道防线改掉)
	h := []retriever.Hit{{Score: 0.6, Chunk: retriever.Chunk{
		ID: 9, Title: "零散材料", Source: "x", Seq: 0, Content: "见附件。",
	}}}
	got := New(nil).Build(context.Background(), "食堂几点开饭", h)
	if got.Mode != ModeNoHit {
		t.Fatalf("抽不到含实词的句子应拒答, 实际 mode=%s text=%s", got.Mode, got.Text)
	}
	if !strings.Contains(got.Text, "没有找到") {
		t.Errorf("拒答文案应明确说明没有依据: %s", got.Text)
	}
}

func TestSnippet_围绕命中词取窗口(t *testing.T) {
	content := "## 一、前面这些内容\n\n这一大段都是与本问题无关的背景说明，用来把关键句推到很后面，验证窗口是不是围绕命中词取的。\n\n### 消防通道\n\n消防通道堵塞属于严重级，要求 15 分钟内到场清理。"
	got := Snippet(content, []string{"消防通道", "到场"}, 80)
	if !strings.Contains(got, "消防通道") {
		t.Errorf("片段应包含命中词附近的内容: %s", got)
	}
	if strings.HasPrefix(got, "一、前面这些内容") {
		t.Errorf("片段不应从段首截取: %s", got)
	}
	if strings.Contains(got, "**") || strings.Contains(got, "###") {
		t.Errorf("片段应清掉 markdown 符号: %s", got)
	}
}
