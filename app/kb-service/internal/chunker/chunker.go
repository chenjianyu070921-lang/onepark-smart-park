// Package chunker 文档切片: 把一篇材料切成语义相对完整的小段.
//
// 为什么切片策略值得单独成包、单独写测试:
//
//	RAG 的效果上限几乎由切片决定 —— 切碎了检索不到, 切大了引用不精准、还浪费上下文.
//	这里的选择是"按标题层级 + 段落"合并, 而不是固定字符数硬切:
//	  - 带标题路径(Heading) -> 引用时能说清"出自哪一节", 也方便人回原文核对;
//	  - 段落是作者本来划分的意义单元, 顺着它切最不容易切坏句子;
//	  - 只有单段超长时才按句号硬拆.
//
// 被刻意丢掉的东西(写清楚, 免得被问住):
//
//	围栏代码块(``` ... ```)在业务问答里是噪声(命中后引用一段代码没有说服力),
//	所以整块剔除; 表格分隔行(|---|)同理。
package chunker

import (
	"regexp"
	"strings"
)

// Chunk 一个切片.
type Chunk struct {
	Seq     int    // 文档内序号, 从 0 开始
	Heading string // 所属标题路径, 如 "四、定时任务 > 三层防重复"
	Content string
}

// Options 切片参数.
type Options struct {
	MaxLen int // 单切片最大字符数(超过则按句拆)
	MinLen int // 小于此长度的碎片直接丢弃
}

// DefaultOptions 默认参数.
// 400 字: 中文技术/流程文档里大约 2~4 个段落, 足够承载一个完整的意思,
// 又不至于让引用片段长到读不完。
func DefaultOptions() Options { return Options{MaxLen: 400, MinLen: 30} }

var (
	reFence     = regexp.MustCompile("(?s)```.*?```")
	reHeader    = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reTableLine = regexp.MustCompile(`^\s*\|[\s\-:|]+\|\s*$`)
	reQuote     = regexp.MustCompile(`^\s*>\s?`)
)

// Split 把 markdown 正文切成切片.
func Split(markdown string, opt Options) []Chunk {
	if opt.MaxLen <= 0 {
		opt = DefaultOptions()
	}
	text := strings.ReplaceAll(markdown, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = reFence.ReplaceAllString(text, "\n")

	var chunks []Chunk
	headings := map[int]string{} // 层级 -> 标题
	var buf []string             // 待合并的段落
	curHeading := ""
	seq := 0

	flush := func() {
		if len(buf) == 0 {
			return
		}
		body := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
		if body == "" {
			return
		}
		for _, piece := range hardSplit(body, opt.MaxLen) {
			p := strings.TrimSpace(piece)
			if len([]rune(p)) < opt.MinLen {
				continue
			}
			chunks = append(chunks, Chunk{Seq: seq, Heading: curHeading, Content: p})
			seq++
		}
	}

	var para []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if m := reHeader.FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			title := strings.TrimSpace(m[2])
			headings[level] = title
			for k := range headings {
				if k > level {
					delete(headings, k)
				}
			}
			curHeading = joinHeadings(headings, level)
			continue
		}
		if reTableLine.MatchString(line) {
			continue
		}
		line = reQuote.ReplaceAllString(line, "")
		if strings.TrimSpace(line) == "" {
			if len(para) > 0 {
				buf = append(buf, strings.Join(para, "\n"))
				para = para[:0]
			}
			// 段落之间累积到接近 MaxLen 就落一片
			if runeLen(strings.Join(buf, "\n")) >= opt.MaxLen*3/4 {
				flush()
			}
			continue
		}
		para = append(para, line)
	}
	if len(para) > 0 {
		buf = append(buf, strings.Join(para, "\n"))
	}
	flush()

	// 重新编号(丢弃过短的碎片后序号要连续, 否则引用里的"第 N 段"会跳号)
	for i := range chunks {
		chunks[i].Seq = i
	}
	return chunks
}

// hardSplit 单段超长时按句末标点切,(不切坏句子).
func hardSplit(s string, max int) []string {
	if runeLen(s) <= max {
		return []string{s}
	}
	var out []string
	var cur []rune
	for _, r := range []rune(s) {
		cur = append(cur, r)
		if (r == '。' || r == '！' || r == '？' || r == '；' || r == '\n') && len(cur) >= max {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func joinHeadings(headings map[int]string, level int) string {
	var parts []string
	for i := 1; i <= level; i++ {
		if t, ok := headings[i]; ok && t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " > ")
}

func runeLen(s string) int { return len([]rune(s)) }
