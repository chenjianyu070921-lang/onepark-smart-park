// Package answer 由检索结果组织回答.
//
// 三种模式(都会写进问答日志的 mode 字段, 便于效果评估区分):
//
//	no_hit     没有找到依据 -> **明确拒答**, 并给出改进建议。宁可不答, 不要幻觉。
//	extractive 抽取式: 直接引用检索到的原句, 附出处。零依赖、离线可用、结果可复现。
//	llm        生成式: 把检索到的材料交给大模型组织语言, 要求"只依据材料、标注来源"。
//	           失败(超时/无 key/报错) -> 自动回退 extractive, 问答不失败。
//
// 为什么默认就是可用的抽取式: 答辩现场可能没有外网、没有 key, 而"能回答 + 有出处 + 不编造"
// 这三条才是企业知识库的价值底线; 生成式只是把它说得更顺耳。
package answer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"onepark/app/kb-service/internal/llm"
	"onepark/app/kb-service/internal/retriever"
)

// 模式常量.
const (
	ModeNoHit      = "no_hit"
	ModeExtractive = "extractive"
	ModeLLM        = "llm"
)

// Result 回答结果.
type Result struct {
	Text string
	Mode string
}

// Builder 回答构造器.
type Builder struct {
	llm llm.Client // 可为 nil(未启用大模型)
}

func New(llmClient llm.Client) *Builder { return &Builder{llm: llmClient} }

// Build 生成回答.
func (b *Builder) Build(ctx context.Context, question string, hits []retriever.Hit) Result {
	if len(hits) == 0 {
		return Result{Text: noHitText(question), Mode: ModeNoHit}
	}
	if b.llm != nil {
		text, _, err := b.llm.Generate(ctx, systemPrompt, userPrompt(question, hits))
		if err == nil && strings.TrimSpace(text) != "" {
			return Result{Text: text, Mode: ModeLLM}
		}
		// 失败不返回错误: 静默回退抽取式(降级路径要留着, 不然大模型一变慢, 问答就不可用)
	}
	return Result{Text: extractive(question, hits), Mode: ModeExtractive}
}

// ---------- 抽取式 ----------

func extractive(question string, hits []retriever.Hit) string {
	terms := retriever.Tokenize(question)

	var conclusions []string
	// 每个切片只取**最相关的一句**: 同一段里并列取两句, 读起来会像两段无关的话拼在一起
	// (实测: "问：合同到期了... 问：租户欠费多久会被限制服务？" 这种)。命中按相关度排序,
	// 所以结论天然是"最相关的先说"。
	for _, h := range hits {
		if s := bestSentences(h.Chunk.Content, terms, 1); len(s) > 0 {
			conclusions = append(conclusions, s...)
		}
		if len(conclusions) >= 2 {
			break
		}
	}
	if len(conclusions) == 0 { // 命中但挑不出完整句 -> 直接给片段首句
		conclusions = append(conclusions, firstSentence(hits[0].Chunk.Content))
	}

	var sb strings.Builder
	sb.WriteString("【结论】")
	for _, c := range conclusions {
		sb.WriteString(clean(c))
	}
	sb.WriteString("\n\n【依据】\n")
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. 《%s》第 %d 段（%s）\n   %s\n",
			i+1, h.Chunk.Title, h.Chunk.Seq, sourceOf(h.Chunk), snippet(h.Chunk.Content, 160))
	}
	sb.WriteString("\n【说明】以上内容全部摘自知识库原文，未做改写或推断；点击引用可查看完整片段。")
	return sb.String()
}

// bestSentences 按"覆盖了多少提问词"挑出最相关的 n 句, 保持原文顺序返回。
func bestSentences(content string, terms []string, n int) []string {
	sentences := splitSentences(content)
	type scored struct {
		idx   int
		score int
		text  string
	}
	var list []scored
	for i, s := range sentences {
		if len([]rune(s)) < 8 {
			continue
		}
		st := retriever.Tokenize(s)
		set := make(map[string]struct{}, len(st))
		for _, t := range st {
			set[t] = struct{}{}
		}
		score := 0
		for _, t := range terms {
			if _, ok := set[t]; ok {
				score++
			}
		}
		if score > 0 {
			list = append(list, scored{idx: i, score: score, text: s})
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	// FAQ 语料优先选"答"那句: 企业知识库大量内容是「问：… 答：…」体,
	// 按词覆盖度打分时"问"那句天然更贴近用户提问, 于是结论会变成把问题复述一遍
	// —— 用户要的是答案, 所以有"答"句时优先用它(实测踩到)。
	answered := make([]scored, 0, len(list))
	for _, s := range list {
		if !strings.HasPrefix(strings.TrimSpace(clean(s.text)), "问：") {
			answered = append(answered, s)
		}
	}
	if len(answered) > 0 {
		list = answered
	}
	if len(list) > n {
		list = list[:n]
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].idx < list[j].idx })
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.text)
	}
	return out
}

// splitSentences 按中文句末标点切句(保留标点)。
//
// 另外把 FAQ 的「问：」「答：」当成分句点: 不切的话一问一答会连成一句,
// 抽取时就会把问题和答案一起吐出来。
func splitSentences(s string) []string {
	s = strings.ReplaceAll(s, "答：", "\n答：")
	s = strings.ReplaceAll(s, "问：", "\n问：")
	var out []string
	var cur []rune
	for _, r := range []rune(s) {
		cur = append(cur, r)
		if r == '。' || r == '！' || r == '？' || r == '；' || r == '\n' {
			t := strings.TrimSpace(string(cur))
			if t != "" {
				out = append(out, t)
			}
			cur = cur[:0]
		}
	}
	if t := strings.TrimSpace(string(cur)); t != "" {
		out = append(out, t)
	}
	return out
}

func firstSentence(s string) string {
	ss := splitSentences(s)
	if len(ss) == 0 {
		return snippet(s, 80)
	}
	return ss[0]
}

func noHitText(question string) string {
	return fmt.Sprintf("知识库里没有找到能支撑这个问题（%s）的材料，因此不作回答 —— 无依据的回答对企业知识库是负资产。\n\n"+
		"可以试试：\n"+
		"1. 换更具体的说法（带上模块名、区域、单据号，例如「工单超时没人接怎么处理」）；\n"+
		"2. 确认相关文档已录入（在「知识库」页搜索文档标题）；\n"+
		"3. 补充材料后重问 —— 文档录入即刻生效，无需重启服务。", question)
}

func snippet(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// clean 去掉 markdown 排版符号(列表符 / 行首编号 / 加粗 / 引用符).
//
// 为什么必须做: 语料是 markdown, 直接拼出来会得到 "- **问：工单派出去没人接怎么办？**" 这种
// 带符号的句子 —— 读起来像没处理干净的中间产物, 演示时很减分。
func clean(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), ">"))
	for _, p := range []string{"- ", "* ", "• "} {
		s = strings.TrimPrefix(strings.TrimSpace(s), p)
	}
	// 行首编号("1. " / "2、")也去掉
	s = strings.TrimSpace(s)
	head := []rune(s)
	i := 0
	for i < len(head) && head[i] >= '0' && head[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(head) && (head[i] == '.' || head[i] == '、') {
		s = strings.TrimSpace(string(head[i+1:]))
	}
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

func sourceOf(c retriever.Chunk) string {
	if c.Source != "" {
		return c.Source
	}
	return "未标注出处"
}

// ---------- 生成式提示词 ----------

const systemPrompt = `你是园区运营知识库助手。回答必须严格遵守：
1) 只依据【材料】作答，材料里没有的内容一律不补充、不推测；
2) 每个结论后用 [序号] 标注它来自哪条材料；
3) 材料不足以回答时，直接说"知识库中没有相关依据"，并建议补充什么材料；
4) 用简洁中文，先说结论再说依据，不要客套话。`

func userPrompt(question string, hits []retriever.Hit) string {
	var sb strings.Builder
	sb.WriteString("【材料】\n")
	for i, h := range hits {
		fmt.Fprintf(&sb, "[%d]（%s · 第 %d 段）%s\n", i+1, h.Chunk.Title, h.Chunk.Seq, h.Chunk.Content)
	}
	sb.WriteString("\n【问题】")
	sb.WriteString(question)
	sb.WriteString("\n\n请依据材料回答，并按 [序号] 标注出处。")
	return sb.String()
}
