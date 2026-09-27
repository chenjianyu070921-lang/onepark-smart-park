package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 测试替身: 权限与通行记录数据访问层(不依赖真实 MySQL)
// ---------------------------------------------------------------------------

type fakePermissionStore struct {
	granted []*model.AccessPermission
	delCnt  int64
	err     error
	// effective 为远程开门授权校验返回的授权记录; nil 表示"无授权".
	effective *model.AccessPermission
}

func (f *fakePermissionStore) Grant(_ context.Context, permissions []*model.AccessPermission) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.granted = append(f.granted, permissions...)
	return int64(len(permissions)), nil
}

func (f *fakePermissionStore) Revoke(_ context.Context, _ int64, _ []int64, _ []string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.delCnt, nil
}

func (f *fakePermissionStore) FindEffective(context.Context, int64, int64, string) (*model.AccessPermission, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.effective, nil
}

var _ model.PermissionModel = (*fakePermissionStore)(nil)

type fakeRecordStore struct {
	list      []*model.AccessRecord
	total     int64
	err       error
	gotFilter model.AccessRecordFilter
	// created 捕获写入的通行记录(远程开门写入链路).
	created []*model.AccessRecord
}

func (f *fakeRecordStore) List(_ context.Context, req model.AccessRecordFilter) ([]*model.AccessRecord, int64, error) {
	f.gotFilter = req
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.list, f.total, nil
}

func (f *fakeRecordStore) Create(_ context.Context, r *model.AccessRecord) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, r)
	return nil
}

var _ model.RecordModel = (*fakeRecordStore)(nil)

func newAccessCtx(perms *fakePermissionStore, records *fakeRecordStore) *svc.ServiceContext {
	return &svc.ServiceContext{
		Config:      config.Config{RemoteOpen: config.RemoteOpenConf{AllowedDeviceIds: []string{allowedDevice}}},
		Permissions: perms,
		Records:     records,
	}
}

// ---------------------------------------------------------------------------
// #45 授权
// ---------------------------------------------------------------------------

// TestGrantAccess_CartesianProduct 2 人 × 2 设备应生成 4 条权限, 且全部带上租户.
func TestGrantAccess_CartesianProduct(t *testing.T) {
	store := &fakePermissionStore{}
	l := NewGrantAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))

	resp, err := l.GrantAccess(&types.GrantAccessReq{PersonIds: []int64{101, 102}, DeviceIds: []string{"door-01", "door-02"}})
	if err != nil {
		t.Fatalf("授权应成功: %v", err)
	}
	if resp.Granted != 4 {
		t.Errorf("期望 4 条, 实际 %d", resp.Granted)
	}
	if len(store.granted) != 4 {
		t.Fatalf("实际写入 %d 条", len(store.granted))
	}
	for _, p := range store.granted {
		if p.TenantID != 7 {
			t.Errorf("权限缺失租户隔离: %+v", p)
		}
		if p.Status != model.PermissionStatusValid {
			t.Errorf("新授权应为有效态: %+v", p)
		}
	}
}

// TestGrantAccess_DedupAndFilter 重复/非法的人员与设备需去重过滤.
func TestGrantAccess_DedupAndFilter(t *testing.T) {
	store := &fakePermissionStore{}
	l := NewGrantAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))

	resp, err := l.GrantAccess(&types.GrantAccessReq{
		PersonIds: []int64{101, 101, 0, -3}, // 去重 + 过滤非正数
		DeviceIds: []string{"door-01", "door-01", "  ", ""},
	})
	if err != nil {
		t.Fatalf("授权应成功: %v", err)
	}
	if resp.Granted != 1 {
		t.Errorf("去重后应只剩 1 条, 实际 %d", resp.Granted)
	}
	if store.granted[0].PersonID != 101 || store.granted[0].DeviceID != "door-01" {
		t.Errorf("保留的权限组合错误: %+v", store.granted[0])
	}
}

// TestGrantAccess_TimeWindowAndExpire 时间段与过期时间的合法/非法分支.
func TestGrantAccess_TimeWindowAndExpire(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	cases := []struct {
		name   string
		req    types.GrantAccessReq
		expire string
		window *types.TimeWindow
		ok     bool
	}{
		{name: "合法时间段+过期", window: &types.TimeWindow{Start: "08:00", End: "20:00", Days: []int64{1, 2, 3}}, expire: future, ok: true},
		{name: "时间格式非法", window: &types.TimeWindow{Start: "8:00", End: "20:00", Days: []int64{1}}, ok: false},
		{name: "days 越界", window: &types.TimeWindow{Start: "08:00", End: "20:00", Days: []int64{8}}, ok: false},
		{name: "days 为空", window: &types.TimeWindow{Start: "08:00", End: "20:00"}, ok: false},
		{name: "过期时间格式非法", expire: "2027-09-13", ok: false},
		{name: "过期时间已过去", expire: time.Now().Add(-time.Hour).Format(time.RFC3339), ok: false},
	}

	for _, c := range cases {
		store := &fakePermissionStore{}
		l := NewGrantAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))
		req := types.GrantAccessReq{PersonIds: []int64{101}, DeviceIds: []string{"door-01"}, TimeWindow: c.window, ExpireAt: c.expire}

		_, err := l.GrantAccess(&req)
		if c.ok && err != nil {
			t.Errorf("%s: 期望成功, 实际 %v", c.name, err)
			continue
		}
		if !c.ok {
			ce, isCodeErr := err.(*errorx.CodeError)
			if !isCodeErr || ce.Code != errorx.ErrAccessParamInvalid {
				t.Errorf("%s: 期望 %s, 实际 %v", c.name, errorx.ErrAccessParamInvalid, err)
			}
			if len(store.granted) != 0 {
				t.Errorf("%s: 参数非法不应写库", c.name)
			}
		}
	}
}

// TestGrantAccess_ParamInvalid 空列表/缺租户/超上限一律拒绝且不写库.
func TestGrantAccess_ParamInvalid(t *testing.T) {
	cases := map[string]GrantAccessCase{
		"缺少租户":     {tenantID: 0, persons: []int64{101}, devices: []string{"door-01"}},
		"人员为空":     {tenantID: 7, persons: nil, devices: []string{"door-01"}},
		"设备为空":     {tenantID: 7, persons: []int64{101}, devices: nil},
		"列表全为非法值": {tenantID: 7, persons: []int64{-1}, devices: []string{"  "}},
	}
	for name, c := range cases {
		store := &fakePermissionStore{}
		l := NewGrantAccessLogic(newTenantCtx(c.tenantID, 9), newAccessCtx(store, nil))
		if _, err := l.GrantAccess(&types.GrantAccessReq{PersonIds: c.persons, DeviceIds: c.devices}); err == nil {
			t.Errorf("%s: 应返回参数错误", name)
		}
		if len(store.granted) != 0 {
			t.Errorf("%s: 参数非法不应写库", name)
		}
	}

	// 超过单次上限(maxGrantItems)必须被拒绝.
	store := &fakePermissionStore{}
	l := NewGrantAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))
	persons := make([]int64, 0, 40)
	for i := 0; i < 40; i++ {
		persons = append(persons, int64(i+1))
	}
	devices := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		devices = append(devices, "door-"+string(rune('a'+i)))
	}
	if _, err := l.GrantAccess(&types.GrantAccessReq{PersonIds: persons, DeviceIds: devices}); err == nil {
		t.Error("超出单次授权上限应被拒绝")
	}
	if len(store.granted) != 0 {
		t.Error("超限不应写库")
	}
}

// GrantAccessCase 参数校验用例结构体.
type GrantAccessCase struct {
	tenantID int64
	persons  []int64
	devices  []string
}

// TestGrantAccess_StoreError 持久层失败按授权失败码返回(不暴露底层错误细节).
func TestGrantAccess_StoreError(t *testing.T) {
	store := &fakePermissionStore{err: errors.New("mysql down")}
	l := NewGrantAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))

	_, err := l.GrantAccess(&types.GrantAccessReq{PersonIds: []int64{101}, DeviceIds: []string{"door-01"}})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAccessGrant {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAccessGrant, err)
	}
}

// ---------------------------------------------------------------------------
// #46 撤权
// ---------------------------------------------------------------------------

// TestRevokeAccess_NotFound 无匹配记录必须报错, 不能静默返回 0.
func TestRevokeAccess_NotFound(t *testing.T) {
	store := &fakePermissionStore{delCnt: 0}
	l := NewRevokeAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))

	_, err := l.RevokeAccess(&types.RevokeAccessReq{PersonIds: []int64{101}, DeviceIds: []string{"door-01"}})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAccessRevoke {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAccessRevoke, err)
	}
}

// TestRevokeAccess_Success 正常撤权返回删除条数.
func TestRevokeAccess_Success(t *testing.T) {
	store := &fakePermissionStore{delCnt: 3}
	l := NewRevokeAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))

	resp, err := l.RevokeAccess(&types.RevokeAccessReq{PersonIds: []int64{101, 102}, DeviceIds: []string{"door-01"}})
	if err != nil {
		t.Fatalf("撤权应成功: %v", err)
	}
	if resp.Revoked != 3 {
		t.Errorf("期望 3 条, 实际 %d", resp.Revoked)
	}
}

// TestRevokeAccess_ParamInvalid 空列表与缺租户按参数错误处理.
func TestRevokeAccess_ParamInvalid(t *testing.T) {
	store := &fakePermissionStore{delCnt: 1}
	l := NewRevokeAccessLogic(newTenantCtx(0, 9), newAccessCtx(store, nil))
	if _, err := l.RevokeAccess(&types.RevokeAccessReq{PersonIds: []int64{101}, DeviceIds: []string{"door-01"}}); err == nil {
		t.Error("缺少租户应报错")
	}

	l = NewRevokeAccessLogic(newTenantCtx(7, 9), newAccessCtx(store, nil))
	if _, err := l.RevokeAccess(&types.RevokeAccessReq{PersonIds: []int64{101}, DeviceIds: []string{}}); err == nil {
		t.Error("设备列表为空应报错")
	}
}

// ---------------------------------------------------------------------------
// #48 通行记录
// ---------------------------------------------------------------------------

// TestListAccessRecords_FilterForward 筛选条件应正确下传, 且租户来自上下文而非入参.
func TestListAccessRecords_FilterForward(t *testing.T) {
	store := &fakeRecordStore{list: []*model.AccessRecord{
		{ID: 2, TenantID: 7, PersonID: 101, DeviceID: "door-01", Result: model.AccessResultSuccess, OpenType: model.OpenTypeCard, CreatedAt: time.Now()},
		{ID: 1, TenantID: 7, PersonID: 101, DeviceID: "door-01", Result: model.AccessResultFail, OpenType: model.OpenTypeFace, CreatedAt: time.Now()},
	}, total: 2}
	l := NewListAccessRecordsLogic(newTenantCtx(7, 9), newAccessCtx(nil, store))

	resp, err := l.ListAccessRecords(&types.ListAccessRecordsReq{
		PersonId: 101, DeviceId: "door-01", Result: model.AccessResultSuccess,
		OpenType: model.OpenTypeCard, Page: 2, PageSize: 5,
	})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 2 || resp.Page != 2 || resp.PageSize != 5 {
		t.Errorf("分页响应异常: %+v", resp)
	}
	if store.gotFilter.TenantID != 7 {
		t.Errorf("租户未透传: %d", store.gotFilter.TenantID)
	}
	if store.gotFilter.PersonID != 101 || store.gotFilter.DeviceID != "door-01" || store.gotFilter.OpenType != model.OpenTypeCard {
		t.Errorf("筛选条件未透传: %+v", store.gotFilter)
	}
	if store.gotFilter.Result == nil || *store.gotFilter.Result != model.AccessResultSuccess {
		t.Errorf("result 筛选未透传: %+v", store.gotFilter.Result)
	}
}

// TestListAccessRecords_ParamInvalid result/open_type 越界与时间区间倒置必须拒绝.
func TestListAccessRecords_ParamInvalid(t *testing.T) {
	store := &fakeRecordStore{}
	l := NewListAccessRecordsLogic(newTenantCtx(7, 9), newAccessCtx(nil, store))

	// 缺租户场景由 TestListAccessRecords_TenantMissing 单独覆盖(租户来自 ctx, 不是入参).
	cases := map[string]types.ListAccessRecordsReq{
		"result 越界":   {Result: 5},
		"open_type 非法": {OpenType: "fingerprint"},
		"时间区间倒置":      {StartTime: 200, EndTime: 100},
	}
	for name, req := range cases {
		if _, err := l.ListAccessRecords(&req); err == nil {
			t.Errorf("%s: 应返回参数错误", name)
		}
	}
}

// TestListAccessRecords_TenantMissing 缺少租户信息必须拒绝, 避免查全表.
func TestListAccessRecords_TenantMissing(t *testing.T) {
	store := &fakeRecordStore{}
	l := NewListAccessRecordsLogic(ctxdata.SetUserId(context.Background(), 9), newAccessCtx(nil, store))

	_, err := l.ListAccessRecords(&types.ListAccessRecordsReq{Page: 1, PageSize: 10})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAccessParamInvalid {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAccessParamInvalid, err)
	}
}

// TestGrantHelpers_Boundary 时间段与过期时间解析的边界取值.
func TestGrantHelpers_Boundary(t *testing.T) {
	if _, err := marshalTimeWindow(nil); err != nil {
		t.Fatalf("未指定时间段应返回 nil: %v", err)
	}
	w, err := marshalTimeWindow(&types.TimeWindow{Start: "08:00", End: "20:00", Days: []int64{1}})
	if err != nil || w == nil {
		t.Fatalf("合法时间段应序列化成功: %v", err)
	}
	// 边界合法值: 00:00 / 23:59 必须放行.
	if _, err := marshalTimeWindow(&types.TimeWindow{Start: "00:00", End: "23:59", Days: []int64{7}}); err != nil {
		t.Errorf("边界取值应被接受: %v", err)
	}
	// 越界/少位/错符必须拦截.
	for _, start := range []string{"24:00", "8:00", "0800", "12:60", "23:59:01"} {
		if _, err := marshalTimeWindow(&types.TimeWindow{Start: start, End: "20:00", Days: []int64{1}}); err == nil {
			t.Errorf("start=%s 应被拒绝", start)
		}
	}
	if _, err := parseExpireAt(""); err != nil {
		t.Errorf("空过期时间应视为长期有效: %v", err)
	}
}
