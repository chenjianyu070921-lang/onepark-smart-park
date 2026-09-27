package logic

import (
	"errors"
	"testing"
	"time"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/errorx"
)

// configZero 空流配置, 构造 ServiceContext 用(维护面不依赖流地址下发配置).
func configZero() config.StreamConf { return config.StreamConf{} }

// ---------------------------------------------------------------------------
// 地址簿维护: 详情 / 修改 / 删除
// ---------------------------------------------------------------------------

func TestGetCamera_Success(t *testing.T) {
	hb := time.Now().Add(-time.Minute)
	store := &fakeCameraStore{detail: &model.Camera{
		ID: 7, TenantID: 2, Name: "北门", DeviceID: "cam-7", AreaID: 12,
		RtspURL: validRtsp, Status: model.CameraStatusOnline, LastHeartbeatAt: &hb,
	}}
	l := NewGetCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	resp, err := l.GetCamera(&types.IdReq{Id: 7})
	if err != nil {
		t.Fatalf("查询详情应成功: %v", err)
	}
	if resp.RtspUrl != validRtsp || resp.DeviceId != "cam-7" {
		t.Errorf("详情字段回传异常: %+v", resp)
	}
	if resp.LastHeartbeatAt != hb.Unix() {
		t.Errorf("心跳时间未回传: %d", resp.LastHeartbeatAt)
	}
}

func TestGetCamera_NotFound(t *testing.T) {
	l := NewGetCameraLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: model.ErrCameraNotFound}, configZero()))
	_, err := l.GetCamera(&types.IdReq{Id: 404})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoCameraNotFound {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoCameraNotFound, err)
	}
}

// TestUpdateCamera_PartialUpdate 只传什么改什么: 未提供的字段必须保持原值.
// 若退化成"全量覆盖", 只想改地址的调用方会把 name/location 一并抹掉.
func TestUpdateCamera_PartialUpdate(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewUpdateCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	req := &types.UpdateCameraReq{Id: 7, RtspUrl: "rtsp://10.0.0.9:554/live"}
	if _, err := l.UpdateCamera(req); err != nil {
		t.Fatalf("修改应成功: %v", err)
	}
	if store.updatedID != 7 || store.updatedTenant != 2 {
		t.Errorf("修改未按租户+主键定位: id=%d tenant=%d", store.updatedID, store.updatedTenant)
	}
	if store.patch.RtspURL == nil || *store.patch.RtspURL != "rtsp://10.0.0.9:554/live" {
		t.Errorf("rtsp_url 未传入 patch: %+v", store.patch)
	}
	if store.patch.Name != nil || store.patch.Status != nil || store.patch.Location != nil || store.patch.AreaID != nil {
		t.Errorf("未提供的字段不应进入 patch: %+v", store.patch)
	}
}

// TestUpdateCamera_ZeroValuesUpdatable status=0(离线) 与 area_id=0(未分配) 是合法取值,
// 必须能被改得动 —— 用"零值即未传"判断会让这两项永远改不了.
func TestUpdateCamera_ZeroValuesUpdatable(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewUpdateCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	status := model.CameraStatusOffline
	area := int64(0)
	if _, err := l.UpdateCamera(&types.UpdateCameraReq{Id: 7, Status: &status, AreaId: &area}); err != nil {
		t.Fatalf("status=0/area_id=0 应可修改: %v", err)
	}
	if store.patch.Status == nil || *store.patch.Status != model.CameraStatusOffline {
		t.Errorf("status=0 未被下发: %+v", store.patch.Status)
	}
	if store.patch.AreaID == nil || *store.patch.AreaID != 0 {
		t.Errorf("area_id=0 未被下发: %+v", store.patch.AreaID)
	}
}

// TestUpdateCamera_EmptyPatch 一个字段都没传时要报错, 不能静默成功(调用方会以为改了).
func TestUpdateCamera_EmptyPatch(t *testing.T) {
	store := &fakeCameraStore{err: model.ErrCameraNotFound}
	l := NewUpdateCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	_, err := l.UpdateCamera(&types.UpdateCameraReq{Id: 7})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoCameraNotFound {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoCameraNotFound, err)
	}
}

// TestUpdateCamera_BadRtsp 修改路径同样要校验地址格式: 创建期校验挡不住"改成一个坏地址".
func TestUpdateCamera_BadRtsp(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewUpdateCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	_, err := l.UpdateCamera(&types.UpdateCameraReq{Id: 7, RtspUrl: "http://10.0.0.9/live"})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoParamInvalid {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoParamInvalid, err)
	}
	if store.updatedID != 0 {
		t.Error("非法地址不应下发更新")
	}
}

func TestUpdateCamera_StatusOutOfRange(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewUpdateCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	status := int8(9)
	if _, err := l.UpdateCamera(&types.UpdateCameraReq{Id: 7, Status: &status}); err == nil {
		t.Error("status 越界应报错")
	}
}

func TestDeleteCamera_Success(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewDeleteCameraLogic(ctxWithTenant(2), newCtx(store, configZero()))
	resp, err := l.DeleteCamera(&types.IdReq{Id: 7})
	if err != nil {
		t.Fatalf("删除应成功: %v", err)
	}
	if resp.Id != 7 || store.deletedID != 7 || store.deletedTenant != 2 {
		t.Errorf("删除未按租户+主键定位: resp=%+v store=%d/%d", resp, store.deletedID, store.deletedTenant)
	}
}

func TestDeleteCamera_NotFound(t *testing.T) {
	l := NewDeleteCameraLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: model.ErrCameraNotFound}, configZero()))
	_, err := l.DeleteCamera(&types.IdReq{Id: 404})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoCameraNotFound {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoCameraNotFound, err)
	}
}

// TestCameraGuard 三个维护接口共用同一套前置校验, 任何一处漏判都可能造成跨租户读写.
func TestCameraGuard(t *testing.T) {
	broken := &fakeCameraStore{err: errors.New("mysql down")}
	cases := map[string]struct {
		tenant int64
		id     int64
	}{
		"缺少租户": {tenant: 0, id: 7},
		"ID 非法": {tenant: 2, id: 0},
	}
	for name, c := range cases {
		if _, err := NewGetCameraLogic(ctxWithTenant(c.tenant), newCtx(&fakeCameraStore{}, configZero())).
			GetCamera(&types.IdReq{Id: c.id}); err == nil {
			t.Errorf("%s: 详情应报错", name)
		}
		if _, err := NewUpdateCameraLogic(ctxWithTenant(c.tenant), newCtx(broken, configZero())).
			UpdateCamera(&types.UpdateCameraReq{Id: c.id}); err == nil {
			t.Errorf("%s: 修改应报错", name)
		}
		if _, err := NewDeleteCameraLogic(ctxWithTenant(c.tenant), newCtx(&fakeCameraStore{}, configZero())).
			DeleteCamera(&types.IdReq{Id: c.id}); err == nil {
			t.Errorf("%s: 删除应报错", name)
		}
	}
}

// TestCameraGuard_StorageNil 存储未就绪必须报依赖错误, 不能返回空结果假装成功.
func TestCameraGuard_StorageNil(t *testing.T) {
	noStore := &svc.ServiceContext{}
	if _, err := cameraGuard(ctxWithTenant(2), noStore, 7); err == nil {
		t.Error("存储为 nil 时应报错")
	}
}
