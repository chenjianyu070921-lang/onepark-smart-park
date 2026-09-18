package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	// esTimeout 单次 ES 请求超时: 检索是同步接口链路(网关侧对大屏/后台有时延预算),
	// 宁可快速失败降级 MySQL, 也不让请求悬挂.
	esTimeout = 3 * time.Second
	// maxResponseBytes 读取上限, 避免异常大响应打爆内存.
	maxResponseBytes = 4 << 20 // 4MiB
	// maxErrorBodyBytes 错误响应只保留头部, 用于日志定位.
	maxErrorBodyBytes = 512
	// levelBucketSize 等级聚合桶数: 等级只有 1~4 四档, 取 8 留出脏数据余量.
	levelBucketSize = 8
	// defaultPageSize 与 model 侧分页默认值保持一致.
	defaultPageSize = 10
	// maxPageSize 与 model 侧分页上限保持一致.
	maxPageSize = 100
)

// indexMappingTemplate 索引 mapping 模板(docs/m3/04 §7.1).
//
// %s 处填 content 的分词器: 为空串则省略 analyzer 字段, 用 ES 默认分词器(standard).
// 不硬编码 ik_max_word 的原因: compose 起的是官方镜像(不含 ik 插件), 直接指定 ik 会让
// PUT /{index} 以 illegal_argument_exception 失败 —— 表现为"配了检索反而不可用"。
// 因此分词器由配置 ESConf.Analyzer 决定, 且 EnsureIndex 在插件缺失时自动降级(见该方法注释).
const indexMappingTemplate = `{
  "settings": {"number_of_shards": 1, "number_of_replicas": 0},
  "mappings": {
    "properties": {
      "alarm_id":    {"type": "keyword"},
      "tenant_id":   {"type": "long"},
      "alarm_no":    {"type": "keyword"},
      "device_id":   {"type": "keyword"},
      "area_id":     {"type": "long"},
      "event_type":  {"type": "keyword"},
      "level":       {"type": "integer"},
      "status":      {"type": "integer"},
      "content":     {"type": "text"%s},
      "create_time": {"type": "date"}
    }
  }
}`

// buildIndexMapping 按分词器生成 mapping; analyzer 为空则用默认分词器.
func buildIndexMapping(analyzer string) string {
	if a := strings.TrimSpace(analyzer); a != "" {
		return fmt.Sprintf(indexMappingTemplate, `, "analyzer": "`+a+`"`)
	}
	return fmt.Sprintf(indexMappingTemplate, "")
}

// ESClient 基于标准库 HTTP 实现的 Elasticsearch 客户端.
// 不引入第三方 ES SDK: 本服务只用到 建索引/单文档写入/一次组合检索+聚合 三个操作,
// 手写请求体比引入一个大依赖更可控, 也避免与 go-zero 依赖树打架.
type ESClient struct {
	addrs    []string
	username string
	password string
	index    string
	// analyzer content 字段的分词器, 来自配置 ESConf.Analyzer.
	// 空 = ES 默认分词器; 配 ik_max_word / ik_smart 需 ES 已装 ik 插件, 缺失时 EnsureIndex 自动降级.
	analyzer string
	client   *http.Client
}

var _ Searcher = (*ESClient)(nil)

// NewESClient 创建 ES 客户端.
// addrs 为空返回 nil, 表示"未配置 ES", 由调用方走 MySQL 降级路径.
func NewESClient(addrs []string, username, password, index, analyzer string) *ESClient {
	normalized := normalizeAddrs(addrs)
	if len(normalized) == 0 {
		return nil
	}
	if strings.TrimSpace(index) == "" {
		index = DefaultIndex
	}
	return &ESClient{
		addrs:    normalized,
		username: username,
		password: password,
		index:    index,
		analyzer: strings.TrimSpace(analyzer),
		client:   &http.Client{Timeout: esTimeout},
	}
}

// IndexName 返回实际使用的索引名, 供启动日志定位"检索打到了哪个索引".
func (c *ESClient) IndexName() string { return c.index }

// EnsureIndex 幂等创建索引: 已存在(HTTP 400 + resource_already_exists_exception)视为成功.
//
// 分词器降级: 配置了 analyzer(如 ik_max_word)而 ES 未装对应插件时, PUT 会以 400 报
// "analyzer [xx] not found"。此时**用默认分词器重试一次**并记 WARN, 而不是让整个检索不可用 ——
// 默认分词器下中文按单字切分, 检索质量差但功能在; 建索引失败则检索完全不可用。
// 降级只在本次进程内有效: 索引一旦建成, 后续启动都会命中 resource_already_exists 直接返回。
func (c *ESClient) EnsureIndex(ctx context.Context) error {
	body, status, err := c.do(ctx, http.MethodPut, "/"+c.index, json.RawMessage(buildIndexMapping(c.analyzer)))
	if err == nil {
		return nil
	}
	if status == http.StatusBadRequest && strings.Contains(string(body), "resource_already_exists_exception") {
		return nil // 索引已存在: analyzer 以既有 mapping 为准, 改分词器需重建索引
	}
	if c.analyzer != "" && status == http.StatusBadRequest && looksLikeMissingAnalyzer(body) {
		logx.Errorf("[warn] es analyzer %q 不可用(插件未安装?), 降级为默认分词器: %v",
			c.analyzer, truncate(string(body), maxErrorBodyBytes))
		if _, _, err2 := c.do(ctx, http.MethodPut, "/"+c.index, json.RawMessage(buildIndexMapping(""))); err2 == nil {
			return nil
		}
	}
	return err
}

// looksLikeMissingAnalyzer 判断 400 响应是否由"分词器不存在"引起.
// ES 的原文形如: "analyzer [ik_max_word] not found" / "failed to find global analyzer [ik_smart]".
func looksLikeMissingAnalyzer(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "analyzer") &&
		(strings.Contains(s, "not found") || strings.Contains(s, "failed to find"))
}

// Index 按 alarm_id 幂等写入一条告警文档.
// 用 PUT /_doc/{id} 而非 POST: 重放同一告警时覆盖同一条文档, 不会产生重复历史记录.
func (c *ESClient) Index(ctx context.Context, doc Doc) error {
	path := "/" + c.index + "/_doc/" + strconv.FormatInt(doc.AlarmID, 10)
	_, _, err := c.do(ctx, http.MethodPut, path, doc)
	return err
}

// Search 组合条件检索: 时间范围/等级/状态/区域/设备/事件类型 + 等级聚合.
func (c *ESClient) Search(ctx context.Context, q Query) (*Result, error) {
	page, size := normalizePage(q.Page, q.PageSize)

	body, err := json.Marshal(buildQueryBody(q, page, size))
	if err != nil {
		return nil, fmt.Errorf("es build query: %w", err)
	}

	raw, _, err := c.do(ctx, http.MethodPost, "/"+c.index+"/_search", json.RawMessage(body))
	if err != nil {
		return nil, err
	}

	var resp esSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("es decode search response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("es search error: %s", resp.Error.Reason)
	}

	result := &Result{
		Docs:  make([]Doc, 0, len(resp.Hits.Hits)),
		Total: resp.Hits.Total.Value,
	}
	for _, h := range resp.Hits.Hits {
		result.Docs = append(result.Docs, h.Source)
	}
	for _, b := range resp.Aggregations.LevelCount.Buckets {
		level, err := b.Key.Int64()
		if err != nil {
			continue
		}
		result.LevelCounts = append(result.LevelCounts, LevelCount{Level: int8(level), Total: b.DocCount})
	}
	return result, nil
}

// buildQueryBody 组装检索请求体.
// 全部条件走 bool.filter(不参与相关性打分), 排序按 等级降序 + 时间降序, 与 MySQL 侧口径一致.
func buildQueryBody(q Query, page, size int) map[string]any {
	filters := []any{
		// 租户过滤必须下推到 ES: 索引里存了 tenant_id, 不能取回本地再筛(取回再筛会先多读数据, 也更容易漏).
		map[string]any{"term": map[string]any{"tenant_id": q.TenantID}},
	}
	if !q.StartTime.IsZero() || !q.EndTime.IsZero() {
		rng := map[string]any{}
		if !q.StartTime.IsZero() {
			rng["gte"] = q.StartTime.UnixMilli()
		}
		if !q.EndTime.IsZero() {
			rng["lte"] = q.EndTime.UnixMilli()
		}
		filters = append(filters, map[string]any{"range": map[string]any{"create_time": rng}})
	}
	if q.Level != nil {
		filters = append(filters, map[string]any{"term": map[string]any{"level": *q.Level}})
	}
	if q.Status != nil {
		filters = append(filters, map[string]any{"term": map[string]any{"status": *q.Status}})
	}
	if q.AreaID != 0 {
		filters = append(filters, map[string]any{"term": map[string]any{"area_id": q.AreaID}})
	}
	if q.DeviceID != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"device_id": q.DeviceID}})
	}
	if q.EventType != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"event_type": q.EventType}})
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		// 全文检索放 filter 上下文: 排序固定为 等级降序+时间降序, 不按相关性打分,
		// 因此不需要 must(打分)语义; 放 filter 还能复用 ES 的 filter 缓存.
		filters = append(filters, map[string]any{"match": map[string]any{"content": kw}})
	}

	return map[string]any{
		"query":            map[string]any{"bool": map[string]any{"filter": filters}},
		"from":             (page - 1) * size,
		"size":             size,
		"track_total_hits": true, // 默认只精确统计到 1 万, 历史检索必须给准确 total
		"sort": []any{
			map[string]any{"level": map[string]any{"order": "desc"}},
			map[string]any{"create_time": map[string]any{"order": "desc"}},
		},
		"aggs": map[string]any{
			"level_count": map[string]any{
				"terms": map[string]any{"field": "level", "size": levelBucketSize},
			},
		},
	}
}

// do 发送一次 ES 请求; 多地址时按顺序故障转移, 全部失败才返回错误.
// 返回响应体与状态码, 便于调用方对特定状态码做语义处理(如索引已存在).
func (c *ESClient) do(ctx context.Context, method, path string, payload any) ([]byte, int, error) {
	var body []byte
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("es marshal payload: %w", err)
		}
		body = raw
	}

	var lastErr error
	for _, addr := range c.addrs {
		req, err := http.NewRequestWithContext(ctx, method, addr+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("es build request: %w", err)
		}
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.username != "" {
			req.SetBasicAuth(c.username, c.password)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			// 连接层失败(拒绝连接/DNS/超时): 换下一个地址
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		closeErr := resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			continue
		}

		if resp.StatusCode >= http.StatusMultipleChoices {
			return data, resp.StatusCode, fmt.Errorf("es %s %s -> http %d: %s",
				method, path, resp.StatusCode, truncate(string(data), maxErrorBodyBytes))
		}
		return data, resp.StatusCode, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no available es address")
	}
	return nil, 0, fmt.Errorf("es %s %s failed: %w", method, path, lastErr)
}

// normalizeAddrs 清洗地址列表: 去空白、补 http:// 前缀、去尾部斜杠, 丢弃未展开的环境变量占位符.
func normalizeAddrs(addrs []string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" || strings.Contains(a, "${") {
			// go-zero 在环境变量缺失时保留 ${VAR} 字面量, 直接使用会拼出非法 URL.
			continue
		}
		if !strings.Contains(a, "://") {
			a = "http://" + a
		}
		out = append(out, strings.TrimRight(a, "/"))
	}
	return out
}

// normalizePage 修正非法分页参数, 与 model.normalizePage 同规则(页码>=1, 页大小 1~100).
// size<=0 若不修正会被 ES 当成"不返回文档", 表现为"有 total 没列表"的静默异常.
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

// truncate 截断过长文本, 避免错误日志被响应体淹没.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// esSearchResponse 只声明用到的字段.
type esSearchResponse struct {
	Hits struct {
		Total esHitsTotal `json:"total"`
		Hits  []struct {
			Source Doc `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
	Aggregations struct {
		LevelCount struct {
			Buckets []struct {
				Key      json.Number `json:"key"`
				DocCount int64       `json:"doc_count"`
			} `json:"buckets"`
		} `json:"level_count"`
	} `json:"aggregations"`
	Error *esError `json:"error"`
}

// esError ES 返回的业务错误体.
type esError struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// esHitsTotal 兼容 ES 7+/8 的 {"value":N,"relation":"eq"} 与旧版的裸数字两种形态.
type esHitsTotal struct {
	Value    int64
	Relation string
}

func (t *esHitsTotal) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return err
		}
		t.Value, t.Relation = obj.Value, obj.Relation
		return nil
	}
	var n int64
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return err
	}
	t.Value = n
	return nil
}
