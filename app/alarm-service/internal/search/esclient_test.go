package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturedRequest 记录假 ES 收到的请求, 供断言"请求体到底长什么样".
type capturedRequest struct {
	method string
	path   string
	body   string
	user   string
	pass   string
}

// newFakeES 启动一个假 ES: 记录请求并按给定状态码/响应体回包.
func newFakeES(t *testing.T, status int, resp string) (*httptest.Server, *[]capturedRequest) {
	t.Helper()
	captured := &[]capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体失败: %v", err)
		}
		user, pass, _ := r.BasicAuth()
		*captured = append(*captured, capturedRequest{
			method: r.Method, path: r.URL.Path, body: string(body), user: user, pass: pass,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

// newClientFor 基于假 ES 地址构造客户端(索引用默认值).
func newClientFor(srv *httptest.Server) *ESClient {
	c := NewESClient([]string{srv.URL}, "", "", "", "")
	if c == nil {
		panic("测试前置: ES 客户端不应为 nil")
	}
	return c
}

// TestNewESClient_DisabledByConfig 未配置地址(或环境变量未展开)时必须返回 nil, 由调用方降级 MySQL.
func TestNewESClient_DisabledByConfig(t *testing.T) {
	cases := map[string][]string{
		"nil":        nil,
		"空列表":        {},
		"空字符串":       {""},
		"仅空白":        {"   "},
		"未展开的环境变量占位": {"${ALARM_ES_URL}"}, // go-zero 在变量缺失时保留字面量, 不能当成真实地址
	}
	for name, addrs := range cases {
		if c := NewESClient(addrs, "", "", "", ""); c != nil {
			t.Errorf("%s: 应视为未配置 ES 并返回 nil, 实际 %+v", name, c)
		}
	}
}

// TestNewESClient_Normalize 地址规范化: 补协议头、去尾斜杠, 索引名缺省为 alarm_history.
func TestNewESClient_Normalize(t *testing.T) {
	c := NewESClient([]string{" 127.0.0.1:9200/ ", "http://es-2:9200/"}, "elastic", "pwd", "", "")
	if c == nil {
		t.Fatal("应构造出客户端")
	}
	if c.IndexName() != DefaultIndex {
		t.Errorf("索引名缺省应为 %s, 实际 %s", DefaultIndex, c.IndexName())
	}
	if len(c.addrs) != 2 {
		t.Fatalf("应保留 2 个可用地址, 实际 %d: %v", len(c.addrs), c.addrs)
	}
	if c.addrs[0] != "http://127.0.0.1:9200" {
		t.Errorf("地址应补协议头并去掉尾部斜杠, 实际 %s", c.addrs[0])
	}
}

// TestEnsureIndex_CreatesWithMapping 建索引: PUT /{index}, 请求体带 mapping(含租户与时间字段).
func TestEnsureIndex_CreatesWithMapping(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusOK, `{"acknowledged":true,"shards_acknowledged":true}`)
	c := NewESClient([]string{srv.URL}, "elastic", "pwd", "", "")

	if err := c.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("建索引应成功: %v", err)
	}

	req := (*captured)[0]
	if req.method != http.MethodPut || req.path != "/"+DefaultIndex {
		t.Errorf("建索引应 PUT /%s, 实际 %s %s", DefaultIndex, req.method, req.path)
	}
	for _, want := range []string{`"tenant_id"`, `"create_time"`, `"alarm_id"`, `"number_of_shards":1`} {
		if !strings.Contains(req.body, want) {
			t.Errorf("mapping 请求体缺少 %s: %s", want, req.body)
		}
	}
	if req.user != "elastic" || req.pass != "pwd" {
		t.Errorf("配置了账号时应带 Basic Auth, 实际 %s:%s", req.user, req.pass)
	}
}

// TestEnsureIndex_AlreadyExistsIsNotError 索引已存在(400 + resource_already_exists_exception)不是错误.
func TestEnsureIndex_AlreadyExistsIsNotError(t *testing.T) {
	srv, _ := newFakeES(t, http.StatusBadRequest,
		`{"error":{"type":"resource_already_exists_exception","reason":"index [alarm_history] already exists"}}`)
	c := newClientFor(srv)

	if err := c.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("索引已存在应视为成功(幂等), 实际: %v", err)
	}
}

// TestEnsureIndex_OtherErrorPropagates 其他 4xx(如 mapping 写错)必须报错, 不能被"已存在"分支吞掉.
func TestEnsureIndex_OtherErrorPropagates(t *testing.T) {
	srv, _ := newFakeES(t, http.StatusBadRequest,
		`{"error":{"type":"mapper_parsing_exception","reason":"failed to parse mapping"}}`)
	c := newClientFor(srv)

	err := c.EnsureIndex(context.Background())
	if err == nil {
		t.Fatal("mapping 解析失败必须报错")
	}
	if !strings.Contains(err.Error(), "mapper_parsing_exception") {
		t.Errorf("错误信息应带 ES 返回的原因: %v", err)
	}
}

// TestIndex_WritesDocByAlarmID 双写: PUT /{index}/_doc/{alarm_id}, 文档含租户与状态等全字段.
func TestIndex_WritesDocByAlarmID(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusCreated, `{"result":"created"}`)
	c := newClientFor(srv)

	doc := Doc{
		AlarmID: 9001, TenantID: 7, AlarmNo: "AL2026091500000001", DeviceID: "door-01",
		AreaID: 12, EventType: "intrusion", Level: 3, Status: 0, Content: "非法闯入",
		CreateTime: time.Unix(1757736000, 0).UTC(),
	}
	if err := c.Index(context.Background(), doc); err != nil {
		t.Fatalf("写入文档应成功: %v", err)
	}

	req := (*captured)[0]
	if req.method != http.MethodPut {
		t.Errorf("按 alarm_id 幂等写入应用 PUT(重放覆盖同一条), 实际 %s", req.method)
	}
	if req.path != "/"+DefaultIndex+"/_doc/9001" {
		t.Errorf("文档路径应带 alarm_id, 实际 %s", req.path)
	}
	got := decodeJSON(t, req.body)
	for key, want := range map[string]float64{
		"alarm_id": 9001, "tenant_id": 7, "area_id": 12, "level": 3, "status": 0,
	} {
		if got[key] != want {
			t.Errorf("文档字段 %s 应为 %v, 实际 %v", key, want, got[key])
		}
	}
	if got["create_time"] == nil {
		t.Error("文档必须带 create_time, 否则时间范围检索失效")
	}
}

// TestSearch_BuildsDSLAndParsesResult 检索: 过滤条件全部下推 + 解析命中/聚合.
func TestSearch_BuildsDSLAndParsesResult(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusOK, `{
	  "hits": {
	    "total": {"value": 128, "relation": "eq"},
	    "hits": [
	      {"_source": {"alarm_id": 9001, "tenant_id": 7, "alarm_no": "AL1", "device_id": "door-01",
	                   "area_id": 12, "event_type": "intrusion", "level": 3, "status": 2,
	                   "content": "非法闯入", "create_time": "2026-09-13T10:00:00Z"}},
	      {"_source": {"alarm_id": 9002, "tenant_id": 7, "alarm_no": "AL2", "device_id": "door-02",
	                   "area_id": 12, "event_type": "intrusion", "level": 4, "status": 2,
	                   "content": "胁迫报警", "create_time": "2026-09-13T11:00:00Z"}}
	    ]
	  },
	  "aggregations": {"level_count": {"buckets": [{"key": 3, "doc_count": 5}, {"key": 4, "doc_count": 2}]}}
	}`)
	c := newClientFor(srv)

	level, status := int8(3), int8(2)
	start := time.Unix(1757736000, 0).UTC()
	end := time.Unix(1757822400, 0).UTC()
	res, err := c.Search(context.Background(), Query{
		TenantID: 7, StartTime: start, EndTime: end,
		Level: &level, Status: &status, AreaID: 12, DeviceID: "door-01", EventType: "intrusion",
		Page: 2, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("检索应成功: %v", err)
	}

	req := (*captured)[0]
	if req.method != http.MethodPost || req.path != "/"+DefaultIndex+"/_search" {
		t.Errorf("检索应 POST /%s/_search, 实际 %s %s", DefaultIndex, req.method, req.path)
	}
	raw, err := json.Marshal(decodeJSON(t, req.body))
	if err != nil {
		t.Fatalf("重新序列化请求体失败: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		`"tenant_id":7`, `"level":3`, `"status":2`, `"area_id":12`,
		`"device_id":"door-01"`, `"event_type":"intrusion"`,
		`"gte":1757736000000`, `"lte":1757822400000`,
		`"from":10`, `"size":10`, `"track_total_hits":true`, `"field":"level"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("检索请求体缺少 %s: %s", want, body)
		}
	}

	if res.Total != 128 {
		t.Errorf("命中总数应为 128, 实际 %d", res.Total)
	}
	if len(res.Docs) != 2 {
		t.Fatalf("应解析出 2 条文档, 实际 %d", len(res.Docs))
	}
	if res.Docs[0].AlarmID != 9001 || res.Docs[0].TenantID != 7 || res.Docs[1].Level != 4 {
		t.Errorf("文档字段解析有误: %+v", res.Docs)
	}
	if res.Docs[0].CreateTime.IsZero() {
		t.Error("create_time 应被解析为时间")
	}
	if len(res.LevelCounts) != 2 || res.LevelCounts[0].Level != 3 || res.LevelCounts[0].Total != 5 ||
		res.LevelCounts[1].Level != 4 || res.LevelCounts[1].Total != 2 {
		t.Errorf("等级聚合解析有误: %+v", res.LevelCounts)
	}
}

// TestSearch_NoOptionalFilters 未传可选条件时不应出现多余过滤(否则会静默过滤掉数据).
func TestSearch_NoOptionalFilters(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusOK, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
	c := newClientFor(srv)

	res, err := c.Search(context.Background(), Query{TenantID: 7})
	if err != nil {
		t.Fatalf("检索应成功: %v", err)
	}
	if res.Total != 0 || len(res.Docs) != 0 || len(res.LevelCounts) != 0 {
		t.Errorf("空结果应解析为空: %+v", res)
	}

	// 逐条断言 filter 数组: 字符串包含判断会被 sort/aggs 里的 `"level":` 误伤, 这里精确到过滤条件本身.
	body := decodeJSON(t, (*captured)[0].body)
	query := asMap(t, body["query"], "query")
	filters := asSlice(t, asMap(t, query["bool"], "query.bool")["filter"], "query.bool.filter")
	if len(filters) != 1 {
		t.Fatalf("未传可选条件时应只有租户过滤, 实际 %d 条: %v", len(filters), filters)
	}
	term := asMap(t, asMap(t, filters[0], "filter[0]")["term"], "filter[0].term")
	if len(term) != 1 || term["tenant_id"] != float64(7) {
		t.Errorf("唯一过滤条件应为租户隔离 tenant_id=7, 实际 %v", term)
	}
}

// asMap 断言 JSON 值为对象(测试辅助: 断言失败立即终止, 避免后续 nil 解引用).
func asMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s 应为 JSON 对象, 实际 %T", what, v)
	}
	return m
}

// asSlice 断言 JSON 值为数组.
func asSlice(t *testing.T, v any, what string) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("%s 应为 JSON 数组, 实际 %T", what, v)
	}
	return s
}

// TestSearch_NormalizesPaging 非法分页必须被修正: size<=0 会让 ES 不返回文档(表现为"有总数没列表").
func TestSearch_NormalizesPaging(t *testing.T) {
	cases := []struct {
		name       string
		page, size int
		wantFrom   string
		wantSize   string
	}{
		{"页码为 0", 0, 20, `"from":0`, `"size":20`},
		{"页大小为 0 取默认", 3, 0, `"from":20`, `"size":10`},
		{"页大小超上限被截断", 1, 5000, `"from":0`, `"size":100`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := newFakeES(t, http.StatusOK, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
			client := newClientFor(srv)

			if _, err := client.Search(context.Background(), Query{TenantID: 1, Page: tc.page, PageSize: tc.size}); err != nil {
				t.Fatalf("检索应成功: %v", err)
			}
			body := (*captured)[0].body
			if !strings.Contains(body, tc.wantFrom) || !strings.Contains(body, tc.wantSize) {
				t.Errorf("分页应为 %s %s, 实际请求体: %s", tc.wantFrom, tc.wantSize, body)
			}
		})
	}
}

// TestSearch_TotalAsPlainNumber 兼容旧版 ES 返回裸数字的 total 形态.
func TestSearch_TotalAsPlainNumber(t *testing.T) {
	srv, _ := newFakeES(t, http.StatusOK, `{"hits":{"total":5,"hits":[]}}`)
	c := newClientFor(srv)

	res, err := c.Search(context.Background(), Query{TenantID: 1})
	if err != nil {
		t.Fatalf("检索应成功: %v", err)
	}
	if res.Total != 5 {
		t.Errorf("裸数字 total 应被解析为 5, 实际 %d", res.Total)
	}
}

// TestSearch_HTTPErrorPropagates ES 报错必须上抛, 交由 logic 降级 MySQL 并留痕.
func TestSearch_HTTPErrorPropagates(t *testing.T) {
	srv, _ := newFakeES(t, http.StatusInternalServerError, `{"error":{"reason":"cluster block"}}`)
	c := newClientFor(srv)

	_, err := c.Search(context.Background(), Query{TenantID: 1})
	if err == nil {
		t.Fatal("ES 5xx 必须返回错误")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("错误信息应带状态码: %v", err)
	}
}

// TestSearch_BusinessErrorInBody ES 返回 200 但 body 内含 error 时同样视为失败.
func TestSearch_BusinessErrorInBody(t *testing.T) {
	srv, _ := newFakeES(t, http.StatusOK, `{"error":{"type":"index_not_found_exception","reason":"no such index [alarm_history]"}}`)
	c := newClientFor(srv)

	_, err := c.Search(context.Background(), Query{TenantID: 1})
	if err == nil {
		t.Fatal("body 内 error 必须返回错误")
	}
	if !strings.Contains(err.Error(), "no such index") {
		t.Errorf("错误信息应带 ES 原因: %v", err)
	}
}

// TestSearch_FailoverToNextAddress 多地址时首个不可达应自动切到下一个.
func TestSearch_FailoverToNextAddress(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusOK, `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[]}}`)
	// 先起可用节点再关掉故障节点, 避免 httptest 复用到刚释放的端口.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	c := NewESClient([]string{deadURL, srv.URL}, "", "", "", "")

	res, err := c.Search(context.Background(), Query{TenantID: 1})
	if err != nil {
		t.Fatalf("首个地址不可达时应故障转移到下一个: %v", err)
	}
	if res.Total != 1 || len(*captured) != 1 {
		t.Errorf("应由第二个地址完成检索: total=%d 请求数=%d", res.Total, len(*captured))
	}
}

// TestSearch_AllAddressesDown 全部地址不可达必须报错(不返回空结果, 避免"没有数据"的假象).
func TestSearch_AllAddressesDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	c := NewESClient([]string{deadURL}, "", "", "", "")
	if _, err := c.Search(context.Background(), Query{TenantID: 1}); err == nil {
		t.Fatal("全部地址不可达时必须返回错误")
	}
}

// TestBuildIndexMapping_Analyzer 分词器可配置: 留空用默认分词器, 配置了才写进 mapping.
//
// 这条锁住的是"引入 ik 插件的路径": 官方镜像不含 ik, 硬编码 ik_max_word 会让建索引直接失败;
// 因此必须由配置决定, 且留空时行为与改造前完全一致(向后兼容).
func TestBuildIndexMapping_Analyzer(t *testing.T) {
	// 前提: 未配置时不出现 analyzer 字段(否则未装插件的环境会直接 400).
	plain := buildIndexMapping("")
	if strings.Contains(plain, "analyzer") {
		t.Errorf("未配置分词器时 mapping 不应出现 analyzer 字段: %s", plain)
	}
	if !strings.Contains(plain, `"content":     {"type": "text"}`) {
		t.Errorf("未配置分词器时 content 应为纯 text: %s", plain)
	}

	withIK := buildIndexMapping("ik_max_word")
	if !strings.Contains(withIK, `"analyzer": "ik_max_word"`) {
		t.Errorf("配置了分词器应写入 mapping: %s", withIK)
	}
	if !json.Valid([]byte(withIK)) {
		t.Errorf("生成的 mapping 必须是合法 JSON: %s", withIK)
	}
}

// TestBuildIndexMapping_WhitespaceAnalyzerTrimmed 配置里带空白不应产出非法 JSON.
func TestBuildIndexMapping_WhitespaceAnalyzerTrimmed(t *testing.T) {
	got := buildIndexMapping("  ik_smart  ")
	if !strings.Contains(got, `"analyzer": "ik_smart"`) {
		t.Errorf("分词器应去空白: %s", got)
	}
	if strings.Contains(got, `"ik_smart  "`) {
		t.Errorf("分词器不应保留首尾空白: %s", got)
	}
}

// TestEnsureIndex_FallsBackToDefaultAnalyzer ik 插件缺失时降级为默认分词器, 而不是让检索整体不可用.
//
// 背景: 官方 ES 镜像不含 ik 插件, 配置 ik_max_word 会收到
// 400 "analyzer [ik_max_word] not found"。若直接把错误上抛, 表现是"配了 ES 反而不可用"。
//
// 注意: 假 ES 只在**首次** PUT 返回"analyzer 未找到", 重试返回成功 ——
// 若恒定返回 400, 重试也会失败, 测不出降级路径。
func TestEnsureIndex_FallsBackToDefaultAnalyzer(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var first = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		isFirst := first
		first = false
		mu.Unlock()

		if isFirst {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"illegal_argument_exception","reason":"analyzer [ik_max_word] not found"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer srv.Close()

	c := NewESClient([]string{srv.URL}, "", "", "", "ik_max_word")
	if err := c.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("ik 插件缺失时应降级为默认分词器而非报错: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("应先按 ik 建一次、失败后用默认分词器重试一次, 实际 %d 次", len(bodies))
	}
	if !strings.Contains(bodies[0], "ik_max_word") {
		t.Errorf("首次请求应带配置的分词器: %s", bodies[0])
	}
	if strings.Contains(bodies[1], "analyzer") {
		t.Errorf("重试请求应不带 analyzer(用默认分词器): %s", bodies[1])
	}
}

// TestEnsureIndex_NoFallbackWhenAnalyzerUnset 未配置分词器时不得重试(避免对已存在索引重复 PUT).
func TestEnsureIndex_NoFallbackWhenAnalyzerUnset(t *testing.T) {
	srv, captured := newFakeES(t, http.StatusBadRequest,
		`{"error":{"type":"mapper_parsing_exception","reason":"failed to parse mapping"}}`)
	c := newClientFor(srv)

	if err := c.EnsureIndex(context.Background()); err == nil {
		t.Fatal("未配置分词器时的建索引失败必须原样上抛, 不得静默降级")
	}
	if n := len(*captured); n != 1 {
		t.Errorf("未配置分词器时应只请求一次, 实际 %d 次", n)
	}
}

// TestBuildQueryBody_Keyword 关键词进入检索条件, 且不传时不产生任何额外条件(向后兼容).
func TestBuildQueryBody_Keyword(t *testing.T) {
	base := buildQueryBody(Query{TenantID: 1}, 1, 10)
	baseFilter, _ := base["query"].(map[string]any)
	baseBool, _ := baseFilter["bool"].(map[string]any)
	before := len(baseBool["filter"].([]any))
	if !json.Valid([]byte(mustMarshal(t, base))) {
		t.Fatal("基础请求体应是合法 JSON")
	}

	kw := buildQueryBody(Query{TenantID: 1, Keyword: "温度过高"}, 1, 10)
	raw := mustMarshal(t, kw)
	if !strings.Contains(raw, `"match"`) || !strings.Contains(raw, `"content"`) || !strings.Contains(raw, "温度过高") {
		t.Fatalf("关键词应生成 content 的 match 条件: %s", raw)
	}
	kwFilter, _ := kw["query"].(map[string]any)
	kwBool, _ := kwFilter["bool"].(map[string]any)
	if len(kwBool["filter"].([]any)) != before+1 {
		t.Errorf("关键词应只新增 1 个过滤条件: before=%d after=%d", before, len(kwBool["filter"].([]any)))
	}

	// 空白关键词等同不传: 前端常把空输入框提交成 "  ".
	blank := buildQueryBody(Query{TenantID: 1, Keyword: "   "}, 1, 10)
	if strings.Contains(mustMarshal(t, blank), `"match"`) {
		t.Errorf("纯空白关键词不应产生 match 条件: %s", mustMarshal(t, blank))
	}
}

// mustMarshal 序列化请求体, 供字段级断言; 失败直接 FAIL 以便定位.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(raw)
}

// decodeJSON 将 JSON 文本解析为 map, 供字段级断言.
func decodeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("解析 JSON 失败: %v, body=%s", err, body)
	}
	return m
}
