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
	minTerms int
}

// NewKeyword 构造关键词检索器.
//
// minTerms: 至少命中多少个**实词元**才算"有关联" —— 小语料里单个泛词命中很常见
// (问"如何申请专利", "申请"在语料里出现过就被算命中), 只靠分数阈值拦不住, 必须另设这道闸。
func NewKeyword(load Loader, minScore float64, maxScan, minTerms int) *KeywordRetriever {
	if maxScan <= 0 {
		maxScan = 5000
	}
	if minTerms <= 0 {
		minTerms = 2
	}
	return &KeywordRetriever{load: load, minScore: minScore, maxScan: maxScan, minTerms: minTerms}
}

func (r *KeywordRetriever) Name() string { return "keyword" }

// Retrieve 实现 Retriever.
func (r *KeywordRetriever) Retrieve(ctx context.Context, tenantID uint64, query string, topK int) ([]Hit, error) {
	qTerms := ContentTerms(query)
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
	// 分母 = **全部查询实词元**的 IDF 之和(含在语料里根本不出现的词)。
	//
	// 这个口径是两次实测换来的(2026-10-13, 都记在这):
	//   · 只把"能命中的词"当分母 -> 只命中一个泛词就满分(实测 top=1.0):
	//     问"食堂今天中午吃什么", 蹭到"中午"两个字就算完美匹配 —— 不相关问题被当成了准答案。
	//   · 把"全部词"当分母, 但不滤疑问词 -> 长口语问句被稀释到阈值以下(实测 0.44 < 0.5),
	//     表现是"明明有答案却拒答"。
	// 所以最终方案是: **分母含全部实词**(不让部分命中被放大) + 查询侧先滤掉疑问词/泛词
	// (见 ContentTerms, 把稀释问题从源头上解决) + 再设"最少命中实词数"这道闸(minTerms)。
	var totalIDF float64
	idf := make(map[string]float64, len(qTerms))
	for _, t := range qTerms {
		// df=0 的词按 df=1 封顶, 不按 BM25 公式给它"最高权重"。
		//
		// 为什么(2026-10-13 评测集实测): 中文 2-gram 会把词边界切出噪声词元
		// ("紧急工单|多久|要到场" 会切出"单多""久要"这种不存在的组合), 而 BM25 里
		// df=0 的 IDF 是全场最高 —— 于是问"紧急工单多久要到场", 有答案的句子反被
		// 三个噪声词稀释到阈值以下, 评测集里表现为 4/10 漏答。
		// 语义上: "语料里根本没出现过的词元"对**区分切片**没有贡献, 给它封顶权重更合理,
		// 它是否代表"问到了语料外的东西"由 MinTerms/阈值一起判断。
		dfUse := df[t]
		if dfUse == 0 {
			dfUse = 1
		}
		v := math.Log(1 + (n-float64(dfUse)+0.5)/(float64(dfUse)+0.5))
		idf[t] = v
		totalIDF += v
	}
	if totalIDF <= 0 { // 查询里没有实词 -> 视为没有依据
		return nil, nil
	}

	hits := make([]Hit, 0, len(chunks))
	for i, c := range chunks {
		dl := float64(runeLen(c.Content))
		if dl <= 0 {
			dl = 1
		}
		var got float64
		matched := 0
		for _, t := range qTerms {
			tf := float64(docs[i][t])
			if tf == 0 {
				continue
			}
			matched++
			norm := tf * (k1 + 1) / (tf + k1*(1-b+b*dl/avgdl))
			got += idf[t] * norm
		}
		// 实词命中太少 -> 不算相关。
		// 为什么需要这道闸: 分数阈值管不住"单个泛词命中"(问"如何申请专利", 语料里有"申请"两个字
		// 就会被算命中)。要求至少 2 个实词同时命中, 才当作真的在问语料里的东西。
		if matched < r.minTerms {
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

// stopWords 疑问词 / 泛词 / 语气词.
//
// 只用于**查询侧**过滤, 语料侧分词绝不滤 —— 否则正文里的"怎么处理"会被拆残。
// 为什么必须有它: 这类词几乎出现在每个问句里却不携带领域信息, 而语料里往往没有它们,
// 于是 IDF 被算得很高 —— 不滤的话"今天天气怎么样"也能在制度文档里蹭出高分(实测踩到)。
var stopWords = []string{
	"怎么", "怎么办", "如何", "什么", "哪些", "哪个", "哪里", "多少", "多久",
	"是不是", "会不会", "能不能", "有没有", "可以", "需要", "请问", "一下",
	"现在", "今天", "昨天", "明天", "我们", "你们", "他们", "这个", "那个",
	"为什么", "以及", "或者", "但是", "因为", "所以", "如果", "就是", "还是",
	"的话", "了吗", "怎样", "是否", "属于", "关于", "针对", "一般", "相关",
}

// stopTerms 把停用词也转成词元(2-gram), 与 Query 侧分词口径一致。
var stopTerms = func() map[string]struct{} {
	m := make(map[string]struct{}, len(stopWords)*2)
	for _, w := range stopWords {
		for _, t := range Tokenize(w) {
			m[t] = struct{}{}
		}
	}
	return m
}()

// ContentTerms 查询侧取词: 分词 -> 去停用词 -> 去重.
//
// 去重是必要的: 同一个词重复出现会把分母算两遍, 让覆盖分虚高。
func ContentTerms(query string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, t := range Tokenize(query) {
		if _, stop := stopTerms[t]; stop {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
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
