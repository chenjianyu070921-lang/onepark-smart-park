package logic

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 录像计划层的测试替身
// ---------------------------------------------------------------------------

// fakeRecordPlanStore 录像计划存储替身: err 用于注入故障, list/detail 用于只读场景.
type fakeRecordPlanStore struct {
	created *model.RecordPlan
	detail  *model.RecordPlan
	list    []*model.RecordPlan
	total   int64
	err     error

	patch        model.RecordPlanPatch
	updatedID    int64
	updatedCount int
	deletedID    int64
}

func (f *fakeRecordPlanStore) Create(_ context.Context, p *model.RecordPlan) error {
	if f.err != nil {
		return f.err
	}
	p.ID = 3301
	f.created = p
	return nil
}

func (f *fakeRecordPlanStore) FindByID(context.Context, int64, int64) (*model.RecordPlan, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.detail, nil
}

func (f *fakeRecordPlanStore) Update(_ context.Context, _, id int64, patch model.RecordPlanPatch) error {
	if f.err != nil {
		return f.err
	}
	f.patch = patch
	f.updatedID = id
	f.updatedCount++
	return nil
}

func (f *fakeRecordPlanStore) Delete(_ context.Context, _, id int64) error {
	if f.err != nil {
		return f.err
	}
	f.deletedID = id
	return nil
}

func (f *fakeRecordPlanStore) List(context.Context, model.RecordPlanListFilter) ([]*model.RecordPlan, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.list, f.total, nil
}

func (f *fakeRecordPlanStore) ListEnabledByCamera(context.Context, int64, int64) ([]*model.RecordPlan, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.list == nil {
		return []*model.RecordPlan{}, nil
	}
	return f.list, nil
}

var _ model.RecordPlanModel = (*fakeRecordPlanStore)(nil)

// newRecordCtx 构造带摄像头 + 录像计划存储的服务上下文.
func newRecordCtx(cameras *fakeCameraStore, plans *fakeRecordPlanStore, record config.RecordConf) *svc.ServiceContext {
	return &svc.ServiceContext{
		Config:      config.Config{Record: record, Stream: config.StreamConf{ExpiresSeconds: 3600}},
		Cameras:     cameras,
		RecordPlans: plans,
	}
}

func enabledStatus() *int8 {
	s := model.RecordPlanStatusEnabled
	return &s
}

// expectCodeErr 断言返回的是指定错误码的业务错误.
func expectCodeErr(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回错误码 %s, 实际 nil", code)
	}
	ce, ok := err.(*errorx.CodeError)
	if !ok {
		t.Fatalf("期望业务错误 %s, 实际 %v", code, err)
	}
	if ce.Code != code {
		t.Fatalf("期望错误码 %s, 实际 %s (%s)", code, ce.Code, ce.Msg)
	}
}

func mustErrCreate(t *testing.T, l *CreateRecordPlanLogic) error {
	t.Helper()
	_, err := l.CreateRecordPlan(&types.CreateRecordPlanReq{CameraId: 1, Name: "x"})
	if err == nil {
		t.Fatal("期望返回错误")
	}
	return err
}

// ---------------------------------------------------------------------------
// 创建录像计划
// ---------------------------------------------------------------------------

func TestCreateRecordPlan_Success(t *testing.T) {
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	plans := &fakeRecordPlanStore{}
	l := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	resp, err := l.CreateRecordPlan(&types.CreateRecordPlanReq{
		CameraId: 2001, Name: "全天录像", Status: enabledStatus(),
	})
	if err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if resp.Id != 3301 {
		t.Errorf("应返回新建ID, 实际 %d", resp.Id)
	}
	if plans.created.TenantID != 2 {
		t.Errorf("缺少租户隔离: %+v", plans.created)
	}
	if plans.created.Strategy != model.RecordStrategyAlways {
		t.Errorf("未指定 strategy 应默认全天, 实际 %q", plans.created.Strategy)
	}
	if plans.created.Status != model.RecordPlanStatusEnabled {
		t.Errorf("状态应为用户显式传入的值, 实际 %d", plans.created.Status)
	}
}

// TestCreateRecordPlan_DefaultStatusEnabled 不传 status 时默认启用:
// 计划"建了却不录"是极难察觉的静默失效, 默认必须是对用户有用的那一侧.
func TestCreateRecordPlan_DefaultStatusEnabled(t *testing.T) {
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}}
	plans := &fakeRecordPlanStore{}
	l := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	if _, err := l.CreateRecordPlan(&types.CreateRecordPlanReq{CameraId: 1, Name: "x"}); err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if plans.created.Status != model.RecordPlanStatusEnabled {
		t.Errorf("默认应为启用(1), 实际 %d", plans.created.Status)
	}
	if plans.created.RetentionDays != defaultRetentionDays {
		t.Errorf("未指定保留天数应取默认 %d, 实际 %d", defaultRetentionDays, plans.created.RetentionDays)
	}
}

// TestCreateRecordPlan_ScheduledNormalized 定时计划的生效日被归一, 保留天数取配置默认值.
func TestCreateRecordPlan_ScheduledNormalized(t *testing.T) {
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}}
	plans := &fakeRecordPlanStore{}
	// 配置默认 3 天, 用于区分"配置生效"与"代码兜底 7 天".
	l := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{DefaultRetentionDays: 3}))

	_, err := l.CreateRecordPlan(&types.CreateRecordPlanReq{
		CameraId: 1, Name: "工作日录像", Strategy: model.RecordStrategyScheduled,
		DaysOfWeek: "5,1,1", StartMinute: 9 * 60, EndMinute: 18 * 60,
	})
	if err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if plans.created.DaysOfWeek != "1,5" {
		t.Errorf("生效日应升序去重归一为 \"1,5\", 实际 %q", plans.created.DaysOfWeek)
	}
	if plans.created.RetentionDays != 3 {
		t.Errorf("保留天数应取配置默认 3, 实际 %d", plans.created.RetentionDays)
	}
}

// TestCreateRecordPlan_AlwaysClearsTimingFields 全天计划不得残留定时字段:
// 留着会让"策略=全天"与"起止分钟"互相打架, 回放推导不知道该听谁的.
func TestCreateRecordPlan_AlwaysClearsTimingFields(t *testing.T) {
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}}
	plans := &fakeRecordPlanStore{}
	l := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	if _, err := l.CreateRecordPlan(&types.CreateRecordPlanReq{
		CameraId: 1, Name: "x", Strategy: model.RecordStrategyAlways,
		DaysOfWeek: "1,2", StartMinute: 600, EndMinute: 1080,
	}); err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if plans.created.DaysOfWeek != "" || plans.created.StartMinute != 0 || plans.created.EndMinute != 0 {
		t.Errorf("全天计划应清空定时字段, 实际 days=%q start=%d end=%d",
			plans.created.DaysOfWeek, plans.created.StartMinute, plans.created.EndMinute)
	}
}

// TestCreateRecordPlan_RejectsIllegalParams 非法入参必须落到 W 级(HTTP 400)且不落库.
func TestCreateRecordPlan_RejectsIllegalParams(t *testing.T) {
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}}
	badStatus := int8(7)
	longName := strings.Repeat("名", 65)

	cases := map[string]types.CreateRecordPlanReq{
		"摄像头为0": {Name: "x"},
		"名称为空":  {CameraId: 1},
		"策略非法":  {CameraId: 1, Name: "x", Strategy: "weekend"},
		"生效日越界": {
			CameraId: 1, Name: "x", Strategy: model.RecordStrategyScheduled,
			DaysOfWeek: "8", StartMinute: 0, EndMinute: 60,
		},
		"起止倒置": {
			CameraId: 1, Name: "x", Strategy: model.RecordStrategyScheduled,
			StartMinute: 600, EndMinute: 60,
		},
		"跨零点": {
			CameraId: 1, Name: "x", Strategy: model.RecordStrategyScheduled,
			StartMinute: 1320, EndMinute: 360,
		},
		"保留期超限": {CameraId: 1, Name: "x", RetentionDays: 99999},
		"状态非法":  {CameraId: 1, Name: "x", Status: &badStatus},
		"名称超长":  {CameraId: 1, Name: longName},
	}
	for name, req := range cases {
		plans := &fakeRecordPlanStore{}
		l := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))
		_, err := l.CreateRecordPlan(&req)
		if err == nil {
			t.Errorf("%s: 应被拒绝", name)
			continue
		}
		ce, ok := err.(*errorx.CodeError)
		if !ok || !strings.Contains(ce.Code, "-W-") {
			t.Errorf("%s: 参数类错误必须返回 W 级(HTTP 400), 实际 %v", name, err)
		}
		if plans.created != nil {
			t.Errorf("%s: 非法参数不应落库", name)
		}
	}

	// 缺少租户单独构造(租户信息来自 ctx, 不在请求体里).
	plans := &fakeRecordPlanStore{}
	l := NewCreateRecordPlanLogic(ctxWithTenant(0), newRecordCtx(cameras, plans, config.RecordConf{}))
	expectCodeErr(t, mustErrCreate(t, l), errorx.ErrVideoParamInvalid)
}

// TestCreateRecordPlan_StorageErrors 摄像头不存在 / 计划重名 / 其它故障的错误码区分.
func TestCreateRecordPlan_StorageErrors(t *testing.T) {
	notFound := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(
		&fakeCameraStore{err: model.ErrCameraNotFound}, &fakeRecordPlanStore{}, config.RecordConf{}))
	expectCodeErr(t, mustErrCreate(t, notFound), errorx.ErrVideoCameraNotFound)

	duplicated := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(
		&fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}},
		&fakeRecordPlanStore{err: model.ErrRecordPlanDuplicate}, config.RecordConf{}))
	expectCodeErr(t, mustErrCreate(t, duplicated), errorx.ErrVideoRecordPlanCreate)

	broken := NewCreateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(
		&fakeCameraStore{detail: &model.Camera{ID: 1, TenantID: 2}},
		&fakeRecordPlanStore{err: errors.New("mysql down")}, config.RecordConf{}))
	expectCodeErr(t, mustErrCreate(t, broken), errorx.ErrVideoRecordPlanCreate)
}

// ---------------------------------------------------------------------------
// 列表 / 详情
// ---------------------------------------------------------------------------

func TestListRecordPlans_StatusFilter(t *testing.T) {
	created := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	plans := &fakeRecordPlanStore{
		list: []*model.RecordPlan{
			{ID: 2, TenantID: 2, CameraID: 1, Name: "B", Strategy: model.RecordStrategyScheduled,
				StartMinute: 540, EndMinute: 1080, Status: model.RecordPlanStatusEnabled, CreatedAt: created},
			{ID: 1, TenantID: 2, CameraID: 1, Name: "A", Strategy: model.RecordStrategyAlways,
				Status: model.RecordPlanStatusDisabled, CreatedAt: created},
		},
		total: 2,
	}
	l := NewListRecordPlansLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))

	resp, err := l.ListRecordPlans(&types.ListRecordPlansReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 2 {
		t.Fatalf("分页响应异常: %+v", resp)
	}
	if resp.List[0].Id != 2 || resp.List[0].StartMinute != 540 {
		t.Errorf("应按 id 倒序并回传定时字段: %+v", resp.List[0])
	}

	// status=0(停用)必须可筛选, 负数表示全部.
	if _, err := l.ListRecordPlans(&types.ListRecordPlansReq{Status: model.RecordPlanStatusDisabled, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=0 应可筛选: %v", err)
	}
	if _, err := l.ListRecordPlans(&types.ListRecordPlansReq{Status: -1, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=-1 应表示不筛选: %v", err)
	}
	if _, err := l.ListRecordPlans(&types.ListRecordPlansReq{Status: 5, Page: 1, PageSize: 10}); err == nil {
		t.Error("status 越界应报错")
	}
}

func TestGetRecordPlan_NotFound(t *testing.T) {
	l := NewGetRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{},
		&fakeRecordPlanStore{err: model.ErrRecordPlanNotFound}, config.RecordConf{}))
	if _, err := l.GetRecordPlan(&types.IdReq{Id: 404}); err == nil {
		t.Fatal("期望返回错误")
	} else {
		expectCodeErr(t, err, errorx.ErrVideoRecordPlanNotFound)
	}
}

// ---------------------------------------------------------------------------
// 修改 / 删除
// ---------------------------------------------------------------------------

// TestUpdateRecordPlan_SwitchToAlwaysResetsTiming 策略改回全天时, 残留的定时字段必须被复位.
func TestUpdateRecordPlan_SwitchToAlwaysResetsTiming(t *testing.T) {
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	plans := &fakeRecordPlanStore{detail: &model.RecordPlan{
		ID: 7, TenantID: 2, CameraID: 1, Name: "旧计划", Strategy: model.RecordStrategyScheduled,
		DaysOfWeek: "1,2", StartMinute: 540, EndMinute: 1080, RetentionDays: 5,
		Status: model.RecordPlanStatusEnabled, CreatedAt: created,
	}}
	l := NewUpdateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))

	if _, err := l.UpdateRecordPlan(&types.UpdateRecordPlanReq{Id: 7, Strategy: model.RecordStrategyAlways}); err != nil {
		t.Fatalf("修改应成功: %v", err)
	}
	if plans.updatedCount != 1 {
		t.Fatalf("应发生一次更新, 实际 %d", plans.updatedCount)
	}
	if plans.patch.Strategy == nil || *plans.patch.Strategy != model.RecordStrategyAlways {
		t.Errorf("策略应被改为全天: %+v", plans.patch.Strategy)
	}
	if plans.patch.DaysOfWeek == nil || *plans.patch.DaysOfWeek != "" {
		t.Errorf("生效日应被清空: %+v", plans.patch.DaysOfWeek)
	}
	if plans.patch.StartMinute == nil || *plans.patch.StartMinute != 0 {
		t.Errorf("起始分钟应清零: %+v", plans.patch.StartMinute)
	}
	if plans.patch.Status != nil {
		t.Errorf("未传 status 时不应修改状态: %+v", plans.patch.Status)
	}
}

// TestUpdateRecordPlan_RejectsCrossMidnight 改成跨零点时段必须被拒(约定拆两条计划).
func TestUpdateRecordPlan_RejectsCrossMidnight(t *testing.T) {
	plans := &fakeRecordPlanStore{detail: &model.RecordPlan{
		ID: 7, TenantID: 2, Strategy: model.RecordStrategyScheduled,
		StartMinute: 540, EndMinute: 1080, RetentionDays: 5, Status: model.RecordPlanStatusEnabled,
	}}
	l := NewUpdateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))

	start, end := 22*60, 6*60
	if _, err := l.UpdateRecordPlan(&types.UpdateRecordPlanReq{Id: 7, StartMinute: &start, EndMinute: &end}); err == nil {
		t.Fatal("跨零点配置应被拒绝")
	}
	if plans.updatedCount != 0 {
		t.Error("非法配置不应落库")
	}
}

// TestUpdateRecordPlan_NotFound 计划不存在时给出专用错误码, 而不是"写入失败".
func TestUpdateRecordPlan_NotFound(t *testing.T) {
	plans := &fakeRecordPlanStore{err: model.ErrRecordPlanNotFound}
	l := NewUpdateRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))
	if _, err := l.UpdateRecordPlan(&types.UpdateRecordPlanReq{Id: 404}); err == nil {
		t.Fatal("期望返回错误")
	} else {
		expectCodeErr(t, err, errorx.ErrVideoRecordPlanNotFound)
	}
}

func TestDeleteRecordPlan_Success(t *testing.T) {
	plans := &fakeRecordPlanStore{}
	l := NewDeleteRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))
	resp, err := l.DeleteRecordPlan(&types.IdReq{Id: 9})
	if err != nil {
		t.Fatalf("删除应成功: %v", err)
	}
	if resp.Id != 9 || plans.deletedID != 9 {
		t.Errorf("应返回被删ID并实际调用删除: resp=%+v store=%d", resp, plans.deletedID)
	}
}

func TestDeleteRecordPlan_NotFound(t *testing.T) {
	plans := &fakeRecordPlanStore{err: model.ErrRecordPlanNotFound}
	l := NewDeleteRecordPlanLogic(ctxWithTenant(2), newRecordCtx(&fakeCameraStore{}, plans, config.RecordConf{}))
	if _, err := l.DeleteRecordPlan(&types.IdReq{Id: 404}); err == nil {
		t.Fatal("期望返回错误")
	} else {
		expectCodeErr(t, err, errorx.ErrVideoRecordPlanNotFound)
	}
}
