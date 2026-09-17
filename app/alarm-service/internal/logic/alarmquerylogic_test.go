package logic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/search"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 测试替身: 覆盖"ES 命中 / ES 故障降级 / 未配置 ES"三条路径, 不需要真实 ES 与 MySQL
// ---------------------------------------------------------------------------

// fakeQueryStore 记录下推的检索条件, 便于断言"过滤条件真的传到了数据层".
type fakeQueryStore struct {
	list   []*model.Alarm
	total  int64
	counts []model.LevelCount
	err    error

	gotHistory model.AlarmHistoryFilter
	gotList    model.AlarmListFilter

	// 状态流转相关(供 updatealarmstatuslogic_test 使用).
	detail     *model.Alarm // FindByID 返回的告警; nil 时返回 ErrNotFound
	detailErr  error
	ackedID    int64
	resolvedID int64
	transition error
}

func (f *fakeQueryStore) Create(context.Context, *model.Alarm) error { return nil }

func (f *fakeQueryStore) FindByID(context.Context, int64, int64) (*model.Alarm, error) {
	if f.detailErr != nil || f.detail == nil {
		return nil, model.ErrNotFound
	}
	return f.detail, nil
}

func (f *fakeQueryStore) List(_ context.Context, filter model.AlarmListFilter) ([]*model.Alarm, int64, error) {
	f.gotList = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.list, f.total, nil
}

func (f *fakeQueryStore) Ack(_ context.Context, _, id, _ int64, _ string, _ time.Time) error {
	if f.transition != nil {
		return f.transition
	}
	f.ackedID = id
	return nil
}

func (f *fakeQueryStore) Resolve(_ context.Context, _, id, _ int64, _ string, _ time.Time) error {
	if f.transition != nil {
		return f.transition
	}
	f.resolvedID = id
	return nil
}

func (f *fakeQueryStore) CountActive(context.Context, int64, int64, []int32) (int64, []model.LevelCount, error) {
	return 0, nil, nil
}

func (f *fakeQueryStore) SearchHistory(
	_ context.Context, filter model.AlarmHistoryFilter,
) ([]*model.Alarm, int64, []model.LevelCount, error) {
	f.gotHistory = filter
	if f.err != nil {
		return nil, 0, nil, f.err
	}
	return f.list, f.total, f.counts, nil
}

var _ model.AlarmModel = (*fakeQueryStore)(nil)

// fakeSearcher 记录下推的 ES 检索条件, 并按配置返回结果或错误.
type fakeSearcher struct {
	result *search.Result
	err    error
	got    search.Query
}

func (f *fakeSearcher) Index(context.Context, search.Doc) error { return nil }

func (f *fakeSearcher) Search(_ context.Context, q search.Query) (*search.Result, error) {
	f.got = q
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

var _ search.Searcher = (*fakeSearcher)(nil)

// historyReq 返回一份"字段全带"的历史检索请求, 用于断言条件透传.
func historyReq() *types.ListAlarmsReq {
	return &types.ListAlarmsReq{
		StartTime: 1757736000, EndTime: 1757822400,
		Level: 3, Status: 2, AreaId: 12, DeviceId: "door-01", EventType: "intrusion",
		Page: 2, PageSize: 10,
	}
}

// assertCode 断言错误码, 且失败时打印原始错误便于定位.
func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	ce, ok := err.(*errorx.CodeError)
	if !ok {
		t.Fatalf("期望错误码 %s, 实际错误类型 %T: %v", want, err, err)
	}
	if ce.Code != want {
		t.Fatalf("期望错误码 %s, 实际 %s(%s)", want, ce.Code, ce.Msg)
	}
}

// ---------------------------------------------------------------------------
// #41 历史告警检索
// ---------------------------------------------------------------------------

// TestListAlarms_ESHit ES 可用时直接返回检索结果, 不再回查 MySQL.
func TestListAlarms_ESHit(t *testing.T) {
	store := &fakeQueryStore{}
	searcher := &fakeSearcher{result: &search.Result{
		Total: 128,
		Docs: []search.Doc{{
			AlarmID: 9001, TenantID: 7, AlarmNo: "AL1", DeviceID: "door-01", AreaID: 12,
			EventType: "intrusion", Level: 3, Status: 2, Content: "非法闯入",
			CreateTime: time.Unix(1757736000, 0).UTC(),
		}},
		LevelCounts: []search.LevelCount{{Level: 3, Total: 5}, {Level: 4, Total: 2}},
	}}
	l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store, Search: searcher})

	resp, err := l.ListAlarms(historyReq())
	if err != nil {
		t.Fatalf("检索应成功: %v", err)
	}
	if resp.Total != 128 || resp.Page != 2 || resp.PageSize != 10 {
		t.Errorf("分页与总数不符: %+v", resp)
	}
	if len(resp.List) != 1 {
		t.Fatalf("应返回 1 条告警, 实际 %d", len(resp.List))
	}
	item := resp.List[0]
	if item.Id != 9001 || item.AlarmNo != "AL1" || item.Level != 3 || item.Status != 2 ||
		item.DeviceId != "door-01" || item.AreaId != 12 || item.CreatedAt != 1757736000 {
		t.Errorf("告警项字段与 ES 文档不一致: %+v", item)
	}
	if resp.Aggs == nil || resp.Aggs.LevelCount["3"] != 5 || resp.Aggs.LevelCount["4"] != 2 {
		t.Errorf("等级聚合应原样返回: %+v", resp.Aggs)
	}

	// ES 命中时不应再查库(否则降级/命中两条链路的结果会互相打架).
	if store.gotHistory.TenantID != 0 {
		t.Errorf("ES 命中路径不应回查 MySQL: %+v", store.gotHistory)
	}

	q := searcher.got
	if q.TenantID != 7 || q.AreaID != 12 || q.DeviceID != "door-01" || q.EventType != "intrusion" ||
		q.Page != 2 || q.PageSize != 10 {
		t.Errorf("检索条件下推有误: %+v", q)
	}
	if q.Level == nil || *q.Level != 3 || q.Status == nil || *q.Status != 2 {
		t.Errorf("等级/状态筛选应下推(指针非 nil): %+v", q)
	}
	if !q.StartTime.Equal(time.Unix(1757736000, 0)) || !q.EndTime.Equal(time.Unix(1757822400, 0)) {
		t.Errorf("时间范围应下推: %v ~ %v", q.StartTime, q.EndTime)
	}
}

// TestListAlarms_ESFailureFallsBackToMySQL ES 查询失败时降级 MySQL, 且过滤口径原样下推.
func TestListAlarms_ESFailureFallsBackToMySQL(t *testing.T) {
	store := &fakeQueryStore{
		list: []*model.Alarm{{
			BaseModel: model.BaseModel{ID: 9002, TenantID: 7, CreatedAt: time.Unix(1757737000, 0)},
			AlarmNo:   "AL2", DeviceID: "door-02", AreaID: 12, EventType: "intrusion",
			Level: model.AlarmLevelCritical, Status: model.AlarmStatusResolved, Content: "胁迫报警",
		}},
		total:  3,
		counts: []model.LevelCount{{Level: 4, Total: 3}},
	}
	searcher := &fakeSearcher{err: errors.New("es cluster down")}
	l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store, Search: searcher})

	resp, err := l.ListAlarms(historyReq())
	if err != nil {
		t.Fatalf("ES 故障时应降级成功: %v", err)
	}
	if resp.Total != 3 || len(resp.List) != 1 || resp.List[0].Id != 9002 {
		t.Errorf("降级结果应来自 MySQL: %+v", resp)
	}
	if resp.Aggs == nil || resp.Aggs.LevelCount["4"] != 3 {
		t.Errorf("降级路径同样要给等级聚合: %+v", resp.Aggs)
	}

	f := store.gotHistory
	if f.TenantID != 7 || f.AreaID != 12 || f.DeviceID != "door-01" || f.EventType != "intrusion" {
		t.Errorf("降级过滤条件与 ES 侧不一致: %+v", f)
	}
	if f.Level == nil || *f.Level != 3 || f.Status == nil || *f.Status != 2 {
		t.Errorf("降级同样要下推等级/状态: %+v", f)
	}
	if f.StartTime == nil || !f.StartTime.Equal(time.Unix(1757736000, 0)) ||
		f.EndTime == nil || !f.EndTime.Equal(time.Unix(1757822400, 0)) {
		t.Errorf("降级同样要下推时间范围: %+v", f)
	}
	if f.Page != 2 || f.PageSize != 10 {
		t.Errorf("降级同样要下推分页: %+v", f)
	}
}

// TestListAlarms_NoESConfigured 未配置 ES 时直接走 MySQL, 不产生错误.
func TestListAlarms_NoESConfigured(t *testing.T) {
	store := &fakeQueryStore{total: 1}
	l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store})

	resp, err := l.ListAlarms(&types.ListAlarmsReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("未配置 ES 应走 MySQL: %v", err)
	}
	if resp.Total != 1 || resp.Aggs == nil || len(resp.Aggs.LevelCount) != 0 {
		t.Errorf("无聚合数据时应返回空 map 而非 nil: %+v", resp)
	}
	// status 不传时零值即 0(未处理): 这是与规则列表一致的约定, 不是"未筛选".
	if store.gotHistory.Status == nil || *store.gotHistory.Status != model.AlarmStatusPending {
		t.Errorf("不传 status 时应按未处理筛选: %+v", store.gotHistory)
	}
	// 未传的其余条件不应下推.
	if store.gotHistory.Level != nil || store.gotHistory.StartTime != nil || store.gotHistory.EndTime != nil {
		t.Errorf("未传的可选条件不应下推: %+v", store.gotHistory)
	}

	// 显式传负数才表示"全部状态".
	if _, err := l.ListAlarms(&types.ListAlarmsReq{Status: -1, Page: 1, PageSize: 10}); err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if store.gotHistory.Status != nil {
		t.Errorf("status 传负数表示不筛选, 实际 %+v", store.gotHistory.Status)
	}
}

// TestListAlarms_EndToEndWithESClient 用真实 ESClient(打到假 ES 服务)跑通
// "logic -> HTTP 请求 -> 解析响应 -> 组装接口返回"整条链路, 替身测不到序列化与解析.
func TestListAlarms_EndToEndWithESClient(t *testing.T) {
	var mu sync.Mutex
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "hits": {"total": {"value": 9, "relation": "eq"}, "hits": [
		    {"_source": {"alarm_id": 9007, "tenant_id": 7, "alarm_no": "AL7", "device_id": "cam-01",
		                 "area_id": 12, "event_type": "person_detect", "level": 4, "status": 0,
		                 "content": "检测到人员", "create_time": "2026-09-16T10:00:00Z"}}
		  ]},
		  "aggregations": {"level_count": {"buckets": [{"key": 4, "doc_count": 9}]}}
		}`))
	}))
	defer srv.Close()

	svcCtx := &svc.ServiceContext{Search: search.NewESClient([]string{srv.URL}, "", "", "")}
	l := NewListAlarmsLogic(tenantCtx(7), svcCtx)

	resp, err := l.ListAlarms(&types.ListAlarmsReq{Level: 4, Page: 1, PageSize: 10, Status: -1})
	if err != nil {
		t.Fatalf("端到端检索应成功: %v", err)
	}
	mu.Lock()
	path := gotPath
	mu.Unlock()
	if path != "/"+search.DefaultIndex+"/_search" {
		t.Errorf("应请求 %s/_search, 实际 %s", search.DefaultIndex, path)
	}
	if resp.Total != 9 || len(resp.List) != 1 {
		t.Fatalf("响应应与假 ES 返回一致: %+v", resp)
	}
	if resp.List[0].Id != 9007 || resp.List[0].EventType != "person_detect" ||
		resp.List[0].CreatedAt != time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("文档字段解析有误: %+v", resp.List[0])
	}
	if resp.Aggs == nil || resp.Aggs.LevelCount["4"] != 9 {
		t.Errorf("聚合结果应透传: %+v", resp.Aggs)
	}
}

// TestListAlarms_StorageUnavailable ES 与 MySQL 都不可用时按依赖故障码返回.
func TestListAlarms_StorageUnavailable(t *testing.T) {
	// 场景一: 未配置 ES 且无 MySQL
	l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{})
	_, err := l.ListAlarms(historyReq())
	assertCode(t, err, errorx.ErrDepConnect)

	// 场景二: 配置了 ES 但 ES 故障, 且无 MySQL
	l = NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Search: &fakeSearcher{err: errors.New("es down")}})
	_, err = l.ListAlarms(historyReq())
	assertCode(t, err, errorx.ErrDepConnect)
}

// TestListAlarms_ParamInvalid 参数校验必须早于依赖检查, 且非法入参不得触达存储.
func TestListAlarms_ParamInvalid(t *testing.T) {
	cases := map[string]*types.ListAlarmsReq{
		"等级越界":   {Level: 99},
		"状态越界":   {Status: 9},
		"时间区间反转": {StartTime: 1757822400, EndTime: 1757736000},
		"页大小超上限": {PageSize: 5000}, // 超上限属归一化而非报错, 这里仅确认不 panic
	}
	for name, req := range cases {
		store := &fakeQueryStore{}
		searcher := &fakeSearcher{result: &search.Result{}}
		l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store, Search: searcher})

		_, err := l.ListAlarms(req)
		switch name {
		case "页大小超上限":
			if err != nil {
				t.Errorf("%s: 超上限应被归一化而非报错, 实际 %v", name, err)
			}
		default:
			assertCode(t, err, errorx.ErrAlarmParamInvalid)
			if store.gotHistory.TenantID != 0 || searcher.got.TenantID != 0 {
				t.Errorf("%s: 非法入参不应触达 ES/MySQL", name)
			}
		}
	}
}

// TestListAlarms_MissingTenant 缺少网关注入的租户信息时拒绝查询(防跨园区泄漏).
func TestListAlarms_MissingTenant(t *testing.T) {
	l := NewListAlarmsLogic(tenantCtx(0), &svc.ServiceContext{Alarms: &fakeQueryStore{}})
	_, err := l.ListAlarms(historyReq())
	assertCode(t, err, errorx.ErrBadRequest)
}

// TestListAlarms_MySQLFailure 降级查询失败时返回查询失败码.
func TestListAlarms_MySQLFailure(t *testing.T) {
	store := &fakeQueryStore{err: errors.New("mysql down")}
	l := NewListAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store})

	_, err := l.ListAlarms(historyReq())
	assertCode(t, err, errorx.ErrAlarmQuery)
}

// ---------------------------------------------------------------------------
// #38 活跃告警列表
// ---------------------------------------------------------------------------

// TestListActiveAlarms_ForcesPendingStatus 活跃列表的 status 恒为 0, 不接受调用方改写.
func TestListActiveAlarms_ForcesPendingStatus(t *testing.T) {
	store := &fakeQueryStore{
		list: []*model.Alarm{{
			BaseModel: model.BaseModel{ID: 9001, TenantID: 7, CreatedAt: time.Unix(1757736000, 0)},
			AlarmNo:   "AL1", DeviceID: "door-01", AreaID: 12, EventType: "intrusion",
			Level: model.AlarmLevelMajor, Status: model.AlarmStatusPending, Content: "非法闯入",
		}},
		total: 2,
	}
	l := NewListActiveAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store})

	resp, err := l.ListActiveAlarms(&types.ListActiveAlarmsReq{
		Level: 3, AreaId: 12, DeviceId: "door-01", Page: 0, PageSize: 5000,
	})
	if err != nil {
		t.Fatalf("查询活跃告警应成功: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 1 || resp.List[0].Status != model.AlarmStatusPending {
		t.Errorf("活跃列表应只含未处理告警: %+v", resp)
	}
	// 活跃列表不返回聚合(#38 契约里没有 aggs).
	if resp.Aggs != nil {
		t.Errorf("活跃告警列表不应返回聚合: %+v", resp.Aggs)
	}
	// 回显的分页必须是"实际生效"的值, 否则前端按回显值算总页数会算错.
	if resp.Page != 1 || resp.PageSize != 100 {
		t.Errorf("分页应归一化为 page=1/page_size=100, 实际 %d/%d", resp.Page, resp.PageSize)
	}

	f := store.gotList
	if f.Status == nil || *f.Status != model.AlarmStatusPending {
		t.Fatalf("必须固定按未处理筛选: %+v", f)
	}
	if f.TenantID != 7 || f.AreaID != 12 || f.DeviceID != "door-01" || f.Page != 1 || f.PageSize != 100 {
		t.Errorf("过滤条件下推有误: %+v", f)
	}
	if f.Level == nil || *f.Level != 3 {
		t.Errorf("等级筛选应下推: %+v", f)
	}
}

// TestListActiveAlarms_ParamInvalid 非法等级直接拒绝, 不触达存储.
func TestListActiveAlarms_ParamInvalid(t *testing.T) {
	store := &fakeQueryStore{}
	l := NewListActiveAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: store})

	_, err := l.ListActiveAlarms(&types.ListActiveAlarmsReq{Level: 5})
	assertCode(t, err, errorx.ErrAlarmParamInvalid)
	if store.gotList.TenantID != 0 {
		t.Error("非法入参不应触达存储")
	}
}

// TestListActiveAlarms_StorageUnavailable 存储未就绪与查询失败的错误码.
func TestListActiveAlarms_StorageUnavailable(t *testing.T) {
	l := NewListActiveAlarmsLogic(tenantCtx(7), &svc.ServiceContext{})
	_, err := l.ListActiveAlarms(&types.ListActiveAlarmsReq{})
	assertCode(t, err, errorx.ErrDepConnect)

	l = NewListActiveAlarmsLogic(tenantCtx(7), &svc.ServiceContext{Alarms: &fakeQueryStore{err: errors.New("mysql down")}})
	_, err = l.ListActiveAlarms(&types.ListActiveAlarmsReq{})
	assertCode(t, err, errorx.ErrAlarmQuery)
}

// TestListActiveAlarms_MissingTenant 缺少租户信息时拒绝查询.
func TestListActiveAlarms_MissingTenant(t *testing.T) {
	l := NewListActiveAlarmsLogic(tenantCtx(0), &svc.ServiceContext{Alarms: &fakeQueryStore{}})
	_, err := l.ListActiveAlarms(&types.ListActiveAlarmsReq{})
	assertCode(t, err, errorx.ErrBadRequest)
}

// TestAlarmListResp_AggsSerialization 锁定响应序列化:
// 活跃列表(#38)不带 aggs 键, 历史检索(#41)必须带 —— "aggs": null 会让前端多写一层判空.
func TestAlarmListResp_AggsSerialization(t *testing.T) {
	active, err := json.Marshal(&types.AlarmListResp{Total: 1, Page: 1, PageSize: 10, List: []types.AlarmItem{}})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(active), "aggs") {
		t.Errorf("活跃告警列表不应序列化出 aggs 字段: %s", active)
	}

	history, err := json.Marshal(&types.AlarmListResp{
		Total: 5, Page: 1, PageSize: 10,
		Aggs: &types.AlarmLevelAgg{LevelCount: map[string]int64{"3": 5}},
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(history), `"aggs":{"level_count":{"3":5}}`) {
		t.Errorf("历史检索应带等级聚合: %s", history)
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// TestNormalizePaging 分页归一化规则: 页码下限 1, 页大小默认 10 且上限 100.
func TestNormalizePaging(t *testing.T) {
	cases := []struct {
		page, size         int64
		wantPage, wantSize int64
	}{
		{0, 0, 1, 10},
		{-5, -1, 1, 10},
		{3, 20, 3, 20},
		{1, 5000, 1, 100},
	}
	for _, c := range cases {
		page, size := normalizePaging(c.page, c.size)
		if page != c.wantPage || size != c.wantSize {
			t.Errorf("normalizePaging(%d,%d) = (%d,%d), 期望 (%d,%d)",
				c.page, c.size, page, size, c.wantPage, c.wantSize)
		}
	}
}
