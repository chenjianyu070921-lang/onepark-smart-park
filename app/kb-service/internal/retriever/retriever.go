// Package retriever 检索层.
//
// 为什么把检索抽成接口(这是本模块最值得讲的一个设计):
//
//	企业知识库的效果 = 检索质量 × 生成质量, 而"检索"这件事有多种实现:
//	关键词(BM25) / 向量语义 / 两者混合 + 重排。本机答辩环境没有向量库、也没有外网
//	embedding 服务, 所以第一版实现 **keyword**(零依赖、可离线、结果确定可单测),
//	但上层逻辑只依赖 Retriever 接口 —— 换向量检索时:
//	  1) 新增一个实现(把 Query 编码成向量, 在向量库做 ANN 检索);
//	  2) config 里 Retrieval.Engine 从 keyword 改成 vector;
//	  改动就这两处, 问答逻辑、接口、前端、测试全都不动。
//	这与大屏聚合层"依赖倒置 + 可替换 provider"是同一套手法。
//
// 打分口径(为什么不是原始 BM25 分数):
//
//	原始 BM25 是无上界分值, 没法用一个固定阈值判断"到底有没有依据";
//	这里把每个查询词的 BM25 贡献归一化到 0~1 再加权平均, 于是
//	  score ∈ [0,1], 含义接近"这条切片覆盖了这次提问的多大比重",
//	  MinScore=0.5 这种阈值才有可解释性(低于它 = 没依据, 直接拒答)。
package retriever

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Chunk 检索输入的切片(与 model.ChunkRow 字段对齐).
type Chunk struct {
	ID         uint64
	DocumentID uint64
	Seq        int
	Heading    string
	Content    string
	Title      string
	Source     string
}

// Hit 一条命中.
type Hit struct {
	Chunk Chunk
	Score float64 // 0~1, 越大越相关
}

// Retriever 检索接口.
type Retriever interface {
	// Retrieve 返回与 query 最相关的若干切片(已按分数降序、已过滤阈值).
	Retrieve(ctx context.Context, tenantID uint64, query string, topK int) ([]Hit, error)
	// Name 实现名, 打日志/写入响应时用(如 keyword), 便于事后归因
	Name() string
}

// Loader 载入某租户的全部候选切片.
type Loader func(ctx context.Context, tenantID uint64, limit int) ([]Chunk, error)

// KeywordRetriever 关键词检索(BM25 归一化覆盖分).
type KeywordRetriever struct {
	load     Loader
	minScore float64
	maxScan  int
}

// NewKeyword 构造关键词检索器.
func NewKeyword(load Loader, minScore float64, maxScan int) *KeywordRetriever {
	if maxScan <= 0 {
		maxScan = 5000
	}
	return &KeywordRetriever{load: load, minScore: minScore, maxScan: maxScan}
}

func (r *KeywordRetriever) Name() string { return "keyword" }

// Retrieve 实现 Retriever.
func (r *KeywordRetriever) Retrieve(ctx context.Context, tenantID uint64, query string, topK int) ([]Hit, error) {
	qTerms := Tokenize(query)
	if len(qTerms) == 0 {
		return nil, nil
	}
	chunks, err := r.load(ctx, tenantID, r.maxScan)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	docs := make([]map[string]int, len(chunks))
	df := make(map[string]int, len(qTerms))
	totalLen := 0
	for i, c := range chunks {
		tf := map[string]int{}
		for _, t := range Tokenize(c.Content + " " + c.Heading + " " + c.Title) {
			tf[t]++
		}
		docs[i] = tf
		totalLen += runeLen(c.Content)
		for _, t := range qTerms {
			if tf[t] > 0 {
				df[t]++
			}
		}
	}
	n := float64(len(chunks))
	avgdl := float64(totalLen) / n
	if avgdl <= 0 {
		avgdl = 1
	}

	const k1, b = 1.2, 0.75
	// 分母只统计"至少能命中一个切片"的查询词(df>0)。
	//
	// 为什么必须这样(2026-10-13 实测踩到): 口语长句里总有几个词是库里完全没有的
	// (问"工单超时没人接怎么办", "怎么/么办"这种组合语料里就没有), 而 df=0 的词
	// IDF 反而最高 —— 若把它们也算进分母, 匹配得再全也会被稀释到阈值以下,
	// 表现就是"明明有答案却拒答"(实测 score 0.44 < MinScore 0.5)。
	// 归一化口径应为"召回得到的这部分问句覆盖了多少", 而不是"整句字面覆盖了多少"。
	var totalIDF float64
	idf := make(map[string]float64, len(qTerms))
	for _, t := range qTerms {
		if df[t] == 0 {
			continue
		}
		v := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
		idf[t] = v
		totalIDF += v
	}
	if totalIDF <= 0 { // 查询词全都不在任何切片里 -> 没有依据
		return nil, nil
	}

	hits := make([]Hit, 0, len(chunks))
	for i, c := range chunks {
		dl := float64(runeLen(c.Content))
		if dl <= 0 {
			dl = 1
		}
		var got float64
		for _, t := range qTerms {
			tf := float64(docs[i][t])
			if tf == 0 {
				continue
			}
			norm := tf * (k1 + 1) / (tf + k1*(1-b+b*dl/avgdl))
			got += idf[t] * norm
		}
		if got <= 0 {
			continue
		}
		// 归一化到 0~1 并截断: BM25 的 tf 饱和项(tf*(k1+1)/(...))在"短切片 + 同词反复出现"
		// 时会大于 1, 于是分解可能 >1(实测 1.0284)。分数只用于排序与阈值比较,
		// 不参与任何业务计算, 所以直接截断到 1 —— 保证 score ∈ [0,1] 这个对外承诺成立。
		score := got / totalIDF
		if score > 1 {
			score = 1
		}
		if score < r.minScore {
			continue
		}
		hits = append(hits, Hit{Chunk: c, Score: math.Round(score*10000) / 10000})
	}
	// 分数降序; 同分按切片 id 升序, 保证结果稳定可复现
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Chunk.ID < hits[j].Chunk.ID
	})
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// Tokenize 中文按 2-gram 切, 英文/数字按词切(小写).
//
// 为什么用 2-gram 而不是分词: 项目里没有中文分词词典, 而 2-gram 对中文检索的召回率足够好,
// 且实现只有十几行、无外部依赖、行为完全确定(便于单测)。
// 代价: 会引入一些无意义组合, 但被 IDF 压住了 —— 高频组合的权重天然低。
func Tokenize(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var cjk []rune
	var word []rune

	flushWord := func() {
		if len(word) > 0 {
			out = append(out, string(word))
			word = word[:0]
		}
	}
	flushCJK := func() {
		for i := 0; i < len(cjk)-1; i++ {
			out = append(out, string(cjk[i:i+2]))
		}
		if len(cjk) == 1 { // 单字查询词也要能命中
			out = append(out, string(cjk[0]))
		}
		cjk = cjk[:0]
	}

	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return out
}

func runeLen(s string) int { return len([]rune(s)) }
