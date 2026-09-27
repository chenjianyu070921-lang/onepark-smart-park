package svc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/search"
)

// fakeSearcher 记录双写文档, 可注入错误以验证"双写失败不影响主链路".
type fakeSearcher struct {
	docs []search.Doc
	err  error
}

func (f *fakeSearcher) Index(_ context.Context, doc search.Doc) error {
	f.docs = append(f.docs, doc)
	return f.err
}

func (f *fakeSearcher) Search(context.Context, search.Query) (*search.Result, error) {
	return &search.Result{}, nil
}

var _ search.Searcher = (*fakeSearcher)(nil)

// newESContext 构造带 ES 双写的消费链路上下文.
func newESContext(searcher search.Searcher) (*ServiceContext, *fakeAlarmStore) {
	store := &fakeAlarmStore{}
	return &ServiceContext{
		Alarms:   store,
		Dedup:    &fakeDeduper{seen: map[string]bool{}},
		Cooldown: newFakeCooldown(),
		Search:   searcher,
	}, store
}

// TestHandleDeviceEvent_IndexesAlarmToES #44 告警落库后写入 ES 检索副本, 字段与告警记录一致.
func TestHandleDeviceEvent_IndexesAlarmToES(t *testing.T) {
	searcher := &fakeSearcher{}
	svcCtx, store := newESContext(searcher)

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-es")); err != nil {
		t.Fatalf("处理事件应成功: %v", err)
	}
	if len(searcher.docs) != 1 {
		t.Fatalf("应有 1 条文档写入 ES, 实际 %d", len(searcher.docs))
	}

	doc := searcher.docs[0]
	alarm := store.alarms[0]
	if doc.AlarmID != alarm.ID || doc.AlarmNo != alarm.AlarmNo {
		t.Errorf("ES 文档应以 alarm_id 幂等且带业务编号: doc=%+v alarm=%+v", doc, alarm)
	}
	if doc.TenantID != 1 || doc.DeviceID != "door-01" || doc.EventType != EventTypeIntrusion || doc.AreaID != 12 {
		t.Errorf("ES 文档字段与事件不一致: %+v", doc)
	}
	// 等级随门禁闯入口径(2026-09-22)为 AlarmLevelMajor(3 "严重"), 规则①与回退分支同档.
	if doc.Level != model.AlarmLevelMajor || doc.Status != model.AlarmStatusPending {
		t.Errorf("ES 文档等级/状态应与告警一致: %+v", doc)
	}
	if !doc.CreateTime.Equal(alarm.CreatedAt) {
		t.Errorf("ES 文档时间应与告警创建时间一致: %v vs %v", doc.CreateTime, alarm.CreatedAt)
	}
}

// TestHandleDeviceEvent_ESFailureKeepsMainChain 双写失败必须只记日志:
// 若因此返回错误, 消费端会重投, 反而可能产生重复告警; 且检索侧仍可降级 MySQL.
func TestHandleDeviceEvent_ESFailureKeepsMainChain(t *testing.T) {
	svcCtx, store := newESContext(&fakeSearcher{err: errors.New("es cluster down")})

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-es-down")); err != nil {
		t.Fatalf("ES 写失败不应让消费失败: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("告警仍应落库(MySQL 是事实来源), 实际 %d 条", len(store.alarms))
	}
}

// TestHandleDeviceEvent_NoESConfiguredIsNotFailure 未配置 ES 时跳过双写, 链路照常.
func TestHandleDeviceEvent_NoESConfiguredIsNotFailure(t *testing.T) {
	svcCtx, store := newTestContext() // Search 为 nil, 即未配置 ES

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-no-es")); err != nil {
		t.Fatalf("未配置 ES 不应影响消费: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("应正常落库 1 条, 实际 %d 条", len(store.alarms))
	}
}

// TestHandleDeviceEvent_EndToEndDualWrite 用真实 ESClient 打到假 ES 服务:
// 证明"落库成功后确实发出了 ES 写入请求", 而不只是调用了替身.
func TestHandleDeviceEvent_EndToEndDualWrite(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"result":"created"}`))
	}))
	defer srv.Close()

	client := search.NewESClient([]string{srv.URL}, "", "", "", "")
	if client == nil {
		t.Fatal("测试前置: ES 客户端不应为 nil")
	}
	svcCtx, store := newESContext(client)

	if err := svcCtx.HandleDeviceEvent(context.Background(), intrusionEvent(t, "rid-e2e")); err != nil {
		t.Fatalf("处理事件应成功: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("应落库 1 条告警, 实际 %d", len(store.alarms))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 {
		t.Fatalf("应向 ES 发出 1 次写入请求, 实际 %d 次: %v", len(paths), paths)
	}
	if !strings.HasPrefix(paths[0], "/"+search.DefaultIndex+"/_doc/") {
		t.Errorf("写入路径应为 /%s/_doc/{alarm_id}, 实际 %s", search.DefaultIndex, paths[0])
	}
}

// TestHandleDeviceEvent_DuplicateMessageIndexedOnce 被幂等拦截的重复消息不得重复写 ES.
func TestHandleDeviceEvent_DuplicateMessageIndexedOnce(t *testing.T) {
	searcher := &fakeSearcher{}
	svcCtx, store := newESContext(searcher)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svcCtx.HandleDeviceEvent(ctx, intrusionEvent(t, "rid-es-dup")); err != nil {
			t.Fatalf("第 %d 次处理出错: %v", i+1, err)
		}
	}
	if len(store.alarms) != 1 || len(searcher.docs) != 1 {
		t.Fatalf("重复消息应只落库/写 ES 各 1 次, 实际 落库=%d 双写=%d", len(store.alarms), len(searcher.docs))
	}
}
