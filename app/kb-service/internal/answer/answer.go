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
//
// 第二道防线(第一道在检索层): 即使检索命中了切片, 也要能在切片里**抽到含实词的句子**才算有依据。
// 抽不到就拒答 —— "检索到了但答不出"和"没检索到"对用户是同一件事, 都不该硬给一个答案。
func (b *Builder) Build(ctx context.Context, question string, hits []retriever.Hit) Result {
	terms := retriever.ContentTerms(question)
	if len(hits) == 0 || !hasEvidence(hits, terms) {
		return Result{Text: noHitText(question), Mode: ModeNoHit}
	}
	if b.llm != nil {
		text, _, err := b.llm.Generate(ctx, systemPrompt, userPrompt(question, hits))
		if err == nil && strings.TrimSpace(text) != "" {
			return Result{Text: text, Mode: ModeLLM}
		}
		// 失败不返回错误: 静默回退抽取式(降级路径要留着, 不然大模型一变慢, 问答就不可用)
	}
	return Result{Text: extractive(question, terms, hits), Mode: ModeExtractive}
}

// isFieldLike 判断是不是"字段：值"这种表单式短行(不是完整句子, 不适合当结论)。
func isFieldLike(s string) bool {
	if i := strings.Index(s, "："); i > 0 && i <= 12 { // "工单编号：""问题描述：" 这类
		return true
	}
	return false
}

// isDuplicate 判断新句子是否与已选句子"基本在说同一件事"。
//
// 用**包含度**(交/较短者)而不是 Jaccard: 重复出现的形式往往是"长句 + 它的短版本"
// (制度正文一段 + FAQ 小节里同一句的缩写), Jaccard 会被长度差拉低到 0.3 而漏判(实测);
// 包含度问的是"短的那句是不是被长的那句覆盖了", 这才是这里要判断的关系。
func isDuplicate(terms []string, picked [][]string) bool {
	if len(terms) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		set[t] = struct{}{}
	}
	for _, p := range picked {
		other := make(map[string]struct{}, len(p))
		for _, t := range p {
			other[t] = struct{}{}
		}
		inter := 0
		for t := range set {
			if _, ok := other[t]; ok {
				inter++
			}
		}
		shorter := len(set)
		if len(other) < shorter {
			shorter = len(other)
		}
		if shorter > 0 && float64(inter)/float64(shorter) >= 0.6 {
			return true
		}
	}
	return false
}

// minHits 结论句至少要命中几个提问实词。
//
// 提问词多(≥3)时要求 2 个 —— 只命中 1 个的句子往往是表头/字段这种"含词但没信息"的行
// (实测: "级别 典型场景 响应时限"这种表头被选成结论)。提问本身很短时放宽到 1, 否则容易漏答。
func minHits(terms []string) int {
	if len(terms) >= 3 {
		return 2
	}
	return 1
}

// hasEvidence 判断命中的切片里是否存在"含查询实词的完整句子"。
func hasEvidence(hits []retriever.Hit, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	need := minHits(terms)
	for _, h := range hits {
		if len(bestSentences(cleanBlock(h.Chunk.Content), terms, 1, need)) > 0 {
			return true
		}
	}
	return false
}

// ---------- 抽取式 ----------

func extractive(question string, terms []string, hits []retriever.Hit) string {
	var conclusions []string
	// 每个切片只取**最相关的一句**: 同一段里并列取两句, 读起来会像两段无关的话拼在一起
	// (实测: "问：合同到期了... 问：租户欠费多久会被限制服务？" 这种)。命中按相关度排序,
	// 所以结论天然是"最相关的先说"。
	// 结论只从**最相关的那一段**里取(不够再退到第二段)。
	//
	// 为什么收紧到一段: 按"哪段都行"去找, 会从相关但没回答问题的段落里挑出一句看起来像答案的话
	// (实测: 问"合同到期还想续租, 租金怎么算", 结论变成了"入驻率只统计生效中合同" —— 相关的确是相关的,
	// 但没回答问题)。检索分数最高的那一段才是"依据所在", 结论理应来自它。
	var picked [][]string
	minHit := minHits(terms)
	for _, h := range hits {
		// 先清 markdown 再切句 —— 顺序很重要: 反过来会把 "- **" 这种排版残片当成一个句子,
		// 兜底取到它、clean 完是空串, 结论区就成了空白(实测踩到)。
		for _, s := range bestSentences(cleanBlock(h.Chunk.Content), terms, 2, minHit) {
			// FAQ 体去掉"答："前缀 —— 直接念答案更像回答, 而不是摘录
			line := strings.TrimPrefix(clean(s), "答：")
			// 过滤"字段行"与过短句: 工单记录的"工单标题：xxx"这种也含提问词, 但拿它当结论
			// 等于把表单标签念出来(实测: 消防通道问题的第二条结论变成了"工单标题：地下车库消防通道巡检")。
			if isFieldLike(line) || len([]rune(line)) < 12 {
				continue
			}
			// 去掉"基本在说同一件事"的第二句: 制度文档与它的 FAQ 小节常把同一规则写两遍,
			// 不去重就会得到"要求 15 分钟内到场清理"连着念两遍(实测)。
			dup := retriever.Tokenize(line)
			if isDuplicate(dup, picked) {
				continue
			}
			picked = append(picked, dup)
			conclusions = append(conclusions, line)
			if len(conclusions) >= 2 {
				break
			}
		}
		if len(conclusions) > 0 { // 第一段能给出结论就不再往后取
			break
		}
	}
	if len(conclusions) == 0 {
		// 兜底: 取最相关那段的首句(已清洗过的文本)。调用方已保证有证据, 这里只是防止排版问题
		// 把结论区变成空白 —— 空结论比"稍微不完美的一句"更糟: 用户会以为系统答不上来。
		body := cleanBlock(hits[0].Chunk.Content)
		line := strings.TrimSpace(clean(firstSentence(body)))
		if line == "" {
			line = clipRunes([]rune(body), 120)
		}
		conclusions = append(conclusions, line)
	}

	var sb strings.Builder
	sb.WriteString("【结论】")
	sb.WriteString(strings.Join(conclusions, "\n"))
	sb.WriteString("\n\n【依据】\n")
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. 《%s》第 %d 段（%s）\n   %s\n",
			i+1, h.Chunk.Title, h.Chunk.Seq, sourceOf(h.Chunk), Snippet(h.Chunk.Content, terms, 200))
	}
	sb.WriteString("\n【说明】以上内容全部摘自知识库原文，未做改写或推断；展开引用可查看完整片段。")
	return sb.String()
}

// bestSentences 按"覆盖了多少提问词"挑出最相关的 n 句(至少命中 minHit 个实词才入选),
// 结果保持原文顺序返回。
func bestSentences(content string, terms []string, n, minHit int) []string {
	sentences := splitSentences(content)
	type scored struct {
		idx   int
		score int
		text  string
	}
	scores := make(map[int]int, len(sentences))
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
			scores[i] = score
		}
	}

	// FAQ 配对: 选中"问：…"句, 就把紧跟的"答：…"句一并带上, 并让它继承问句的分数。
	//
	// 为什么必须有这一步: FAQ 里答案句自身与提问的字面重叠通常低于问题句(答案是换套说法讲的),
	// 于是"答"句会被 minHit 挡在门外, 结论又退化成复述问题(实测: 结论变成"问：工单派出去没人接怎么办？")。
	// 但「问」与「答」是**结构上**的配对关系, 不依赖字面重叠 —— 这是 FAQ 语料的常识, 不利用就浪费了。
	for i, sc := range scores {
		if sc < minHit || i+1 >= len(sentences) {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(sentences[i]), "问：") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(sentences[i+1]), "答：") && scores[i+1] < sc {
			scores[i+1] = sc
		}
	}

	var list []scored
	for i, sc := range scores {
		if sc >= minHit {
			list = append(list, scored{idx: i, score: sc, text: sentences[i]})
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].idx < list[j].idx
	})
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

// Snippet 生成引用片段: 先清掉 markdown 排版, 再以"最早出现的实词"为中心取窗口。
//
// 为什么不是简单截前 200 字(第一版就是这么干的): 制度文档一段里常含好几个小节,
// 从段首截会把无关内容截进来、把关键句截掉 —— 用户点开引用要看到的是"结论出处在哪一句",
// 所以窗口要围绕命中词, 而不是围绕段首。
func Snippet(content string, terms []string, width int) string {
	if width <= 0 {
		width = 200
	}
	runes := []rune(cleanBlock(content))
	pos := -1
	for _, t := range terms {
		if t == "" {
			continue
		}
		if i := indexRunes(runes, []rune(t)); i >= 0 && (pos < 0 || i < pos) {
			pos = i
		}
	}
	if pos < 0 {
		return clipRunes(runes, width)
	}
	start := pos - width/3
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
		if start = end - width; start < 0 {
			start = 0
		}
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// indexRunes 朴素子串查找(按 rune)。片段很短, 不需要 KMP。
func indexRunes(hay, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func clipRunes(r []rune, n int) string {
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// cleanBlock 逐行清掉 markdown 排版符号(标题/列表/加粗/引用符), **保留换行**。
//
// 保留换行的原因: 表格一行是一条独立信息, 合成一整段后会被当成"一句话"整段抽出来
// (实测: 告警分级那张表被整个塞进结论, 又长又碎)。保留换行后, 切句时每行自成一句,
// 表头这种没信息的行会被"至少命中 2 个实词"的门槛自然挡掉。
func cleanBlock(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		line = clean(line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "\n")
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
	// markdown 表格: 抽到的句子可能是"| 严重 | 消防告警、电梯困人 | 立即，15 分钟到场 |"
	// (级别/时限这类信息本来就写在表里)。竖线在中文里读作顿挫, 换成逗号再压掉重复标点,
	// 念出来就是"严重，消防告警、电梯困人，立即，15 分钟到场"。
	s = strings.ReplaceAll(s, "|", "，")
	for strings.Contains(s, "，，") {
		s = strings.ReplaceAll(s, "，，", "，")
	}
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	// 中文标点两侧不留空格: "严重 ， 消防告警" 读起来是断的
	for _, p := range []string{"，", "、", "。", "；", "：", "？", "！"} {
		s = strings.ReplaceAll(s, " "+p, p)
		s = strings.ReplaceAll(s, p+" ", p)
	}
	s = strings.Trim(s, " ，")
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
