package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/svc"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/rest/pathvar"
)

// 本文件覆盖 #39/#40 契约路由 handler —— alarm-service 首个 handler 层用例。
// 此前只有 logic/model 层有覆盖, 而"HTTP 请求 → req 绑定"这一段恰恰无人把守:
// path 变量没注入时 req.Id 是合法的 0, 断言会失真(见 newActionRequest 注释)。

// statusActionStore 记录被调用的流转动作与入参。
// 嵌入 model.AlarmModel 接口而非实现全部 7 个方法: 未实现的方法一旦被调用立即 panic,
// 这是"实际执行链路偏离预期"的即时信号, 好过返回零值让用例悄悄通过。
type statusActionStore struct {
	model.AlarmModel
	ackCalled     bool
	resolveCalled bool
	tenantID      int64
	id            int64
	operatorID    int64
	remark        string
	// findCalls / findTenantID 记录状态流转成功后补查告警行的次数与租户:
	// WS 广播与通知 M5 都要靠这次补查拿到业务编号 alarm_no.
	findCalls    int
	findTenantID int64
}

// FindByID 状态流转成功后由 broadcast / notifyResolved 调用, 用于取业务编号 alarm_no.
//
// 必须真实返回行数据: 嵌入的是 nil 的 model.AlarmModel, 不实现该方法会直接 nil panic
// (2026-09-22 实证), 而返回零值行又会让"推送与通知都用 alarm_no"这条契约无人把守 ——
// M5 的 dispatch_task.uk_alarm_id 以 alarm_no 为幂等键, 用物理主键会让两边对不上同一条告警.
func (s *statusActionStore) FindByID(_ context.Context, tenantID, id int64) (*model.Alarm, error) {
	s.findCalls++
	s.findTenantID = tenantID
	row := &model.Alarm{
		AlarmNo:   fmt.Sprintf("AL-20260922-%04d", id),
		DeviceID:  "door-01",
		EventType: "intrusion",
		Level:     model.AlarmLevelMajor,
		Status:    model.AlarmStatusResolved,
	}
	row.ID = id
	row.TenantID = tenantID
	return row, nil
}

func (s *statusActionStore) Ack(_ context.Context, tenantID, id, operatorID int64, remark string, _ time.Time) error {
	s.ackCalled = true
	s.tenantID, s.id, s.operatorID, s.remark = tenantID, id, operatorID, remark
	return nil
}

func (s *statusActionStore) Resolve(_ context.Context, tenantID, id, operatorID int64, remark string, _ time.Time) error {
	s.resolveCalled = true
	s.tenantID, s.id, s.operatorID, s.remark = tenantID, id, operatorID, remark
	return nil
}

// newActionRequest 构造携带路径变量与用户上下文的请求。
// path 变量必须手动注入(pathvar.WithVars): 脱离真实 router 时 httptest 不会解析 :id,
// 不注入则 req.Id 恒为 0 —— 而 0 是合法 int64, 用例看起来会"通过"却什么都没验到。
// tenantID/userID 传 0 即表示"调用方未注入", 与 GetXxx() 的零值语义一致。
func newActionRequest(target, bodyStr string, tenantID, userID int64) *http.Request {
	var body io.Reader
	if bodyStr != "" {
		body = strings.NewReader(bodyStr)
	}
	req := httptest.NewRequest(http.MethodPut, target, body)
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	ctx := ctxdata.SetUserId(ctxdata.SetTenantId(req.Context(), tenantID), userID)
	return pathvar.WithVars(req.WithContext(ctx), map[string]string{"id": "321"})
}

// actionResp 解码统一响应体 {code,msg,data}; Data 内联以少写一层类型。
type actionResp struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Id     int64 `json:"id"`
		Status int8  `json:"status"`
	} `json:"data"`
}

func serveAction(t *testing.T, h http.HandlerFunc, target, bodyStr string, tenantID, userID int64) (*httptest.ResponseRecorder, actionResp) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, newActionRequest(target, bodyStr, tenantID, userID))
	var got actionResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应体非合法 JSON: %v (body=%q)", err, rec.Body.String())
	}
	return rec, got
}

func TestAckAlarmHandler_AckOnly(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	rec, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack",
		`{"remark":"已派安保现场核实"}`, 10, 9001)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码应 200, 实际 %d (%s)", rec.Code, rec.Body.String())
	}
	if got.Code != errorx.SuccessCode {
		t.Fatalf("业务码应 %s, 实际 %s (%s)", errorx.SuccessCode, got.Code, got.Msg)
	}
	if !store.ackCalled || store.resolveCalled {
		t.Fatalf("/ack 应只走确认: ackCalled=%v resolveCalled=%v", store.ackCalled, store.resolveCalled)
	}
	// 三者缺一不可: 租户隔离、定位记录、审计到人。
	if store.tenantID != 10 || store.id != 321 || store.operatorID != 9001 {
		t.Errorf("入参透传异常: tenant=%d id=%d operator=%d", store.tenantID, store.id, store.operatorID)
	}
	if store.remark != "已派安保现场核实" {
		t.Errorf("remark 未透传: %q", store.remark)
	}
	if got.Data.Status != model.AlarmStatusAcked {
		t.Errorf("响应状态应为已确认(%d), 实际 %d", model.AlarmStatusAcked, got.Data.Status)
	}
	if got.Data.Id != 321 {
		t.Errorf("响应缺少告警ID: %d", got.Data.Id)
	}
}

func TestResolveAlarmHandler_ResolveOnly(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	rec, got := serveAction(t, ResolveAlarmHandler(svcCtx), "/api/alarm/321/resolve",
		`{"remark":"现场已处理"}`, 10, 9001)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码应 200, 实际 %d (%s)", rec.Code, rec.Body.String())
	}
	if got.Code != errorx.SuccessCode {
		t.Fatalf("业务码应 %s, 实际 %s (%s)", errorx.SuccessCode, got.Code, got.Msg)
	}
	if !store.resolveCalled || store.ackCalled {
		t.Fatalf("/resolve 应只走解决: ackCalled=%v resolveCalled=%v", store.ackCalled, store.resolveCalled)
	}
	if store.id != 321 || store.tenantID != 10 {
		t.Errorf("入参透传异常: id=%d tenant=%d", store.id, store.tenantID)
	}
	if got.Data.Status != model.AlarmStatusResolved {
		t.Errorf("响应状态应为已解决(%d), 实际 %d", model.AlarmStatusResolved, got.Data.Status)
	}
	// Notifier 为 nil 时不表示NOTIFY故障: resolve 主流程仍须成功, 由同一个 code=0 证明。
}

// TestAlarmActionHandler_PathWinsOverBodyAction 路径决定动作, body 里的 action 必须被忽略。
// 这是新增 handler 与 /status 接口唯一的语义差异, 单独锁定:
// 若将来改成"两处都看", 调用方 PUT /ack 却得到"已解决"时状态机无法回退。
func TestAlarmActionHandler_PathWinsOverBodyAction(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	_, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack",
		`{"action":"resolve","remark":"误以为 body 说了算"}`, 10, 9001)

	if got.Code != errorx.SuccessCode {
		t.Fatalf("应正常确认, 实际 code=%s msg=%s", got.Code, got.Msg)
	}
	if !store.ackCalled || store.resolveCalled {
		t.Errorf("路径优先失效: ackCalled=%v resolveCalled=%v", store.ackCalled, store.resolveCalled)
	}
}

// TestAlarmActionHandler_MissingTenant 缺少租户必须 400, 且不得触达存储层。
func TestAlarmActionHandler_MissingTenant(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	rec, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack", `{"remark":"x"}`, 0, 9001)

	// KI-2: 参数类必须 M3-W-1001(400); 返回 500 会把调用方的错误说成服务端故障。
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP 状态码应 400, 实际 %d", rec.Code)
	}
	if got.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("业务码应 %s, 实际 %s", errorx.ErrAlarmParamInvalid, got.Code)
	}
	if store.ackCalled {
		t.Error("校验失败时不应触达存储层")
	}
}

// TestAlarmActionHandler_MissingOperator 缺少操作人同属参数类问题(#39 要记录 ack_by)。
func TestAlarmActionHandler_MissingOperator(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	rec, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack", `{"remark":"x"}`, 10, 0)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HTTP 状态码应 400, 实际 %d", rec.Code)
	}
	if got.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("业务码应 %s, 实际 %s", errorx.ErrAlarmParamInvalid, got.Code)
	}
	if store.ackCalled {
		t.Error("无操作人时不应写入确认")
	}
}

// fakeNotifier 记录发往 M5 的告警事件, 用于断言跨模块标识口径.
type fakeNotifier struct {
	resolved []notify.AlarmEvent
	created  []notify.AlarmEvent
}

func (f *fakeNotifier) AlarmResolved(_ context.Context, ev notify.AlarmEvent) error {
	f.resolved = append(f.resolved, ev)
	return nil
}

func (f *fakeNotifier) AlarmCreated(_ context.Context, ev notify.AlarmEvent) error {
	f.created = append(f.created, ev)
	return nil
}

// TestResolveAlarmHandler_NotifiesM5WithAlarmNo 解决告警后通知 M5 的事件必须带业务编号 alarm_no.
//
// M5 侧 dispatch_task.uk_alarm_id 以 alarm_no 作幂等键: 发物理主键时 M3 与 M5 拿到的是
// 两个不同的值, 自动派单会重复建单或找不到对应告警 —— 且这类不一致只在联调时才暴露.
func TestResolveAlarmHandler_NotifiesM5WithAlarmNo(t *testing.T) {
	store := &statusActionStore{}
	notifier := &fakeNotifier{}
	svcCtx := &svc.ServiceContext{Alarms: store, Notifier: notifier}

	_, got := serveAction(t, ResolveAlarmHandler(svcCtx), "/api/alarm/321/resolve",
		`{"remark":"现场已处理"}`, 10, 9001)
	if got.Code != errorx.SuccessCode {
		t.Fatalf("应解决成功, 实际 code=%s msg=%s", got.Code, got.Msg)
	}
	if len(notifier.created) != 0 {
		t.Errorf("解决链路不应发出产生事件, 实际 %d 条", len(notifier.created))
	}
	if len(notifier.resolved) != 1 {
		t.Fatalf("应向 M5 发出 1 条解决事件, 实际 %d 条", len(notifier.resolved))
	}
	ev := notifier.resolved[0]
	if ev.AlarmID != "AL-20260922-0321" {
		t.Errorf("事件告警标识应为业务编号 alarm_no, 实际 %q", ev.AlarmID)
	}
	if ev.Action != notify.ActionResolved {
		t.Errorf("动作应为 resolve, 实际 %q", ev.Action)
	}
	if ev.TenantID != 10 {
		t.Errorf("事件租户应透传, 实际 %d", ev.TenantID)
	}
	// 补查必须带租户维度: 不带 tenant_id 的查询会读到别的园区的告警并推给别人.
	if store.findTenantID != 10 {
		t.Errorf("补查告警必须按租户过滤, 实际 tenant=%d", store.findTenantID)
	}
	// 广播与通知各补查一次(推送要 alarm_no, 通知也要), 取到的是同一条告警.
	if store.findCalls != 2 {
		t.Errorf("广播与通知各应补查一次, 实际 %d 次", store.findCalls)
	}
}

// TestAckAlarmHandler_BroadcastLookupScopedByTenant 确认后为广播补查告警行必须带租户.
// 未配置 Notifier 时同样成立: 此时广播是唯一的下游动作, 漏掉租户隔离即跨园区泄漏.
func TestAckAlarmHandler_BroadcastLookupScopedByTenant(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store} // Notifier 为 nil

	_, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack", `{"remark":"已知悉"}`, 10, 9001)
	if got.Code != errorx.SuccessCode {
		t.Fatalf("应确认成功, 实际 code=%s msg=%s", got.Code, got.Msg)
	}
	if store.findCalls != 1 {
		t.Fatalf("确认后应只为广播补查一次, 实际 %d 次", store.findCalls)
	}
	if store.findTenantID != 10 {
		t.Errorf("补查必须限制在本园区内, 实际 tenant=%d", store.findTenantID)
	}
}

// TestAlarmActionHandler_EmptyBodyAllowed 契约里 remark 是 optional, 不传 body 不能报错。
func TestAlarmActionHandler_EmptyBodyAllowed(t *testing.T) {
	store := &statusActionStore{}
	svcCtx := &svc.ServiceContext{Alarms: store}

	_, got := serveAction(t, AckAlarmHandler(svcCtx), "/api/alarm/321/ack", "", 10, 9001)

	if got.Code != errorx.SuccessCode {
		t.Fatalf("无 body 应视为空备注正常确认, 实际 code=%s msg=%s", got.Code, got.Msg)
	}
	if !store.ackCalled {
		t.Fatal("无 body 时未确认")
	}
	if store.remark != "" {
		t.Errorf("未传 remark 应为空字符串, 实际 %q", store.remark)
	}
}
