package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type fakeRuleStore struct {
	created  *model.AlarmRule
	updates  map[string]interface{}
	detail   *model.AlarmRule
	list     []*model.AlarmRule
	total    int64
	gotID    int64
	gotTenant int64
	err      error
}

func (f *fakeRuleStore) Create(_ context.Context, r *model.AlarmRule) error {
	if f.err != nil {
		return f.err
	}
	r.ID = 5001
	f.created = r
	return nil
}

func (f *fakeRuleStore) Update(_ context.Context, tenantID, id int64, updates map[string]interface{}) error {
	f.gotTenant, f.gotID, f.updates = tenantID, id, updates
	return f.err
}

func (f *fakeRuleStore) FindByID(_ context.Context, tenantID, id int64) (*model.AlarmRule, error) {
	f.gotTenant, f.gotID = tenantID, id
	if f.err != nil {
		return nil, f.err
	}
	return f.detail, nil
}

func (f *fakeRuleStore) List(_ context.Context, _ model.AlarmRuleListFilter) ([]*model.AlarmRule, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.list, f.total, nil
}

func (f *fakeRuleStore) ListEnabled(context.Context) ([]rule.Rule, error) { return nil, nil }

var _ model.AlarmRuleModel = (*fakeRuleStore)(nil)

func newRuleCtx(store *fakeRuleStore) *svc.ServiceContext {
	return &svc.ServiceContext{Rules: store}
}

func tenantCtx(tenantID int64) context.Context {
	return ctxdata.SetTenantId(context.Background(), tenantID)
}

// int8Ptr 返回 int8 指针, 用于构造"显式传入 status/level"的请求.
func int8Ptr(v int8) *int8 { return &v }

const validConditions = `{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gte","value":80}]}`

// ---------------------------------------------------------------------------
// #34 创建规则
// ---------------------------------------------------------------------------

func TestCreateRule_Success(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewCreateRuleLogic(tenantCtx(3), newRuleCtx(store))

	resp, err := l.CreateRule(&types.CreateRuleReq{
		Name: "温度越限", EventType: "temperature", Level: model.AlarmLevelMajor,
		RuleType: "threshold", Conditions: validConditions, Status: model.RuleStatusEnabled,
	})
	if err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if resp.Id != 5001 {
		t.Errorf("应返回新建规则ID, 实际 %d", resp.Id)
	}
	if store.created.TenantID != 3 {
		t.Errorf("规则缺失租户隔离: %+v", store.created)
	}
	if store.created.RuleType != "threshold" {
		t.Errorf("规则类型异常: %s", store.created.RuleType)
	}
}

// TestCreateRule_AliasNormalized 文档 04 的 composite/window 写法应归一化为引擎类型名.
func TestCreateRule_AliasNormalized(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewCreateRuleLogic(tenantCtx(3), newRuleCtx(store))

	if _, err := l.CreateRule(&types.CreateRuleReq{
		Name: "组合", EventType: "door", Level: 2, RuleType: "composite",
		Conditions: `{"type":"composite","conditions":[{"field":"payload.x","op":"eq","value":1}]}`,
		Status:     1, WindowSeconds: 60,
	}); err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if store.created.RuleType != "combination" {
		t.Errorf("composite 应归一化为 combination, 实际 %s", store.created.RuleType)
	}
}

// TestCreateRule_ParamInvalid 各类非法入参必须被拒且不落库.
func TestCreateRule_ParamInvalid(t *testing.T) {
	cases := map[string]types.CreateRuleReq{
		"名称为空":     {EventType: "e", Level: 2, RuleType: "threshold", Conditions: validConditions, Status: 1},
		"事件类型为空":   {Name: "x", Level: 2, RuleType: "threshold", Conditions: validConditions, Status: 1},
		"等级越界":     {Name: "x", EventType: "e", Level: 9, RuleType: "threshold", Conditions: validConditions, Status: 1},
		"规则类型非法":   {Name: "x", EventType: "e", Level: 2, RuleType: "regexp", Conditions: validConditions, Status: 1},
		"条件 JSON 坏": {Name: "x", EventType: "e", Level: 2, RuleType: "threshold", Conditions: `{"type":`, Status: 1},
		"条件算子非法":   {Name: "x", EventType: "e", Level: 2, RuleType: "threshold", Conditions: `{"type":"threshold","conditions":[{"field":"a","op":"~","value":1}]}`, Status: 1},
		"状态取值非法":   {Name: "x", EventType: "e", Level: 2, RuleType: "threshold", Conditions: validConditions, Status: 5},
		"窗口为负":     {Name: "x", EventType: "e", Level: 2, RuleType: "time_window", Conditions: `{"type":"time_window","threshold":3}`, Status: 1, WindowSeconds: -1},
	}
	for name, req := range cases {
		store := &fakeRuleStore{}
		l := NewCreateRuleLogic(tenantCtx(3), newRuleCtx(store))
		_, err := l.CreateRule(&req)
		if err == nil {
			t.Errorf("%s: 应返回错误", name)
			continue
		}
		ce, ok := err.(*errorx.CodeError)
		if !ok {
			t.Errorf("%s: 错误类型应为 CodeError, 实际 %T", name, err)
			continue
		}
		if store.created != nil {
			t.Errorf("%s: 非法入参不应写库", name)
		}
		if name == "条件 JSON 坏" || name == "条件算子非法" {
			if ce.Code != errorx.ErrAlarmRuleCreate {
				t.Errorf("%s: 条件解析失败应用规则创建错误码, 实际 %s", name, ce.Code)
			}
		} else if ce.Code != errorx.ErrAlarmParamInvalid {
			t.Errorf("%s: 期望 %s, 实际 %s", name, errorx.ErrAlarmParamInvalid, ce.Code)
		}
	}
}

// TestCreateRule_TenantMissing 缺少租户信息必须拒绝(防止规则写进"公共租户").
func TestCreateRule_TenantMissing(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewCreateRuleLogic(tenantCtx(0), newRuleCtx(store))
	_, err := l.CreateRule(&types.CreateRuleReq{
		Name: "x", EventType: "e", Level: 2, RuleType: "threshold", Conditions: validConditions, Status: 1,
	})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAlarmParamInvalid {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAlarmParamInvalid, err)
	}
	if store.created != nil {
		t.Error("缺少租户不应写库")
	}
}

// TestCreateRule_StoreError 持久层失败按规则创建错误码返回.
func TestCreateRule_StoreError(t *testing.T) {
	l := NewCreateRuleLogic(tenantCtx(3), newRuleCtx(&fakeRuleStore{err: errors.New("mysql down")}))
	_, err := l.CreateRule(&types.CreateRuleReq{
		Name: "x", EventType: "e", Level: 2, RuleType: "threshold", Conditions: validConditions, Status: 1,
	})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAlarmRuleCreate {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAlarmRuleCreate, err)
	}
}

// ---------------------------------------------------------------------------
// #35 更新 / 启用禁用
// ---------------------------------------------------------------------------

// TestUpdateRule_Disable status=0 必须能写入(零值更新), 这是启用/禁用开关的核心.
func TestUpdateRule_Disable(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewUpdateRuleLogic(tenantCtx(3), newRuleCtx(store))

	if _, err := l.UpdateRule(&types.UpdateRuleReq{Id: 77, Status: int8Ptr(model.RuleStatusDisabled)}); err != nil {
		t.Fatalf("禁用应成功: %v", err)
	}
	if store.gotID != 77 || store.gotTenant != 3 {
		t.Errorf("更新目标错误: tenant=%d id=%d", store.gotTenant, store.gotID)
	}
	v, ok := store.updates["status"]
	if !ok {
		t.Fatalf("status=0 未进入更新字段表(零值被吞): %+v", store.updates)
	}
	if v != int8(0) {
		t.Errorf("status 值错误: %v", v)
	}
}

// TestUpdateRule_PartialUpdate 未传字段不得进入更新表(避免把列抹成零值).
func TestUpdateRule_PartialUpdate(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewUpdateRuleLogic(tenantCtx(3), newRuleCtx(store))

	if _, err := l.UpdateRule(&types.UpdateRuleReq{Id: 78, Name: "新名称"}); err != nil {
		t.Fatalf("更新应成功: %v", err)
	}
	if _, ok := store.updates["name"]; !ok {
		t.Error("name 未进入更新字段表")
	}
	for _, key := range []string{"status", "level", "event_type", "conditions"} {
		if _, ok := store.updates[key]; ok {
			t.Errorf("未传字段 %s 不应被更新", key)
		}
	}
}

// TestUpdateRule_ParamInvalid 非法字段与空更新必须拒绝.
func TestUpdateRule_ParamInvalid(t *testing.T) {
	store := &fakeRuleStore{}

	bad := map[string]types.UpdateRuleReq{
		"ID 非法":      {Id: 0, Name: "x"},
		"等级越界":       {Id: 1, Level: int8Ptr(8)},
		"状态越界":       {Id: 1, Status: int8Ptr(7)},
		"规则类型非法":     {Id: 1, RuleType: "nope"},
		"条件解析失败":     {Id: 1, Conditions: "not-json"},
		"窗口为负":       {Id: 1, WindowSeconds: -5},
		"无任何更新字段":    {Id: 1},
	}
	for name, req := range bad {
		l := NewUpdateRuleLogic(tenantCtx(3), newRuleCtx(&fakeRuleStore{}))
		if _, err := l.UpdateRule(&req); err == nil {
			t.Errorf("%s: 应返回错误", name)
		}
	}
	if store.updates != nil {
		t.Error("非法参数不应触发更新")
	}
}

// TestUpdateRule_NotFound 规则不存在需返回规则不存在码, 而不是通用失败.
func TestUpdateRule_NotFound(t *testing.T) {
	l := NewUpdateRuleLogic(tenantCtx(3), newRuleCtx(&fakeRuleStore{err: model.ErrRuleNotFound}))
	_, err := l.UpdateRule(&types.UpdateRuleReq{Id: 999, Name: "x"})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAlarmRuleNotFound {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAlarmRuleNotFound, err)
	}
}

// ---------------------------------------------------------------------------
// #36 详情 / #37 列表
// ---------------------------------------------------------------------------

func TestGetRule_SuccessAndNotFound(t *testing.T) {
	now := time.Now()
	store := &fakeRuleStore{detail: &model.AlarmRule{
		BaseModel: model.BaseModel{ID: 9, TenantID: 3, CreatedAt: now, UpdatedAt: now},
		Name: "温度越限", EventType: "temperature", Level: 3, RuleType: "threshold",
		Conditions: validConditions, Status: model.RuleStatusEnabled,
	}}
	l := NewGetRuleLogic(tenantCtx(3), newRuleCtx(store))
	resp, err := l.GetRule(&types.IdReq{Id: 9})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Id != 9 || resp.Name != "温度越限" || resp.Level != 3 || resp.Status != model.RuleStatusEnabled {
		t.Errorf("详情内容异常: %+v", resp)
	}
	if store.gotTenant != 3 {
		t.Error("详情查询未带租户条件")
	}

	notFound := NewGetRuleLogic(tenantCtx(3), newRuleCtx(&fakeRuleStore{err: model.ErrRuleNotFound}))
	if _, err := notFound.GetRule(&types.IdReq{Id: 404}); err == nil {
		t.Error("规则不存在应返回错误")
	}
}

func TestListRules_Success(t *testing.T) {
	now := time.Now()
	store := &fakeRuleStore{
		list: []*model.AlarmRule{
			{BaseModel: model.BaseModel{ID: 2, TenantID: 3, CreatedAt: now, UpdatedAt: now}, Name: "规则B", Status: model.RuleStatusDisabled},
			{BaseModel: model.BaseModel{ID: 1, TenantID: 3, CreatedAt: now, UpdatedAt: now}, Name: "规则A"},
		},
		total: 2,
	}
	l := NewListRulesLogic(tenantCtx(3), newRuleCtx(store))
	resp, err := l.ListRules(&types.ListRulesReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 2 {
		t.Errorf("列表响应异常: %+v", resp)
	}
	if resp.List[0].Name != "规则B" {
		t.Errorf("应按 id DESC 返回: %+v", resp.List[0])
	}
}

// TestListRules_StatusFilter 状态筛选: 0 与 1 都要能传(负数表示不筛选).
func TestListRules_StatusFilter(t *testing.T) {
	store := &fakeRuleStore{}
	l := NewListRulesLogic(tenantCtx(3), newRuleCtx(store))

	if _, err := l.ListRules(&types.ListRulesReq{Status: model.RuleStatusDisabled, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=0(禁用)应可被筛选: %v", err)
	}
	if _, err := l.ListRules(&types.ListRulesReq{Status: -1, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=-1(不筛选)应被接受: %v", err)
	}
	if _, err := l.ListRules(&types.ListRulesReq{Status: 7, Page: 1, PageSize: 10}); err == nil {
		t.Error("status 越界应报错")
	}
}
