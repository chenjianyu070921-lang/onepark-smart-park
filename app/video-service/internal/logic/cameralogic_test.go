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
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type fakeCameraStore struct {
	created *model.Camera
	detail  *model.Camera
	list    []*model.Camera
	total   int64
	err     error

	// 维护面(改/删)与心跳的观测字段
	patch          model.CameraPatch
	updatedID      int64
	updatedTenant  int64
	deletedID      int64
	deletedTenant  int64
	heartbeatOf    string
	matched        int // TouchHeartbeat 返回的匹配条数
	offlineN       int64
	offlineDeadlin time.Time
}

func (f *fakeCameraStore) Create(_ context.Context, c *model.Camera) error {
	if f.err != nil {
		return f.err
	}
	c.ID = 2001
	f.created = c
	return nil
}

func (f *fakeCameraStore) FindByID(context.Context, int64, int64) (*model.Camera, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.detail, nil
}

func (f *fakeCameraStore) List(context.Context, model.CameraListFilter) ([]*model.Camera, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.list, f.total, nil
}

func (f *fakeCameraStore) Update(_ context.Context, tenantID, id int64, patch model.CameraPatch) error {
	if f.err != nil {
		return f.err
	}
	f.patch = patch
	f.updatedID = id
	f.updatedTenant = tenantID
	return nil
}

func (f *fakeCameraStore) Delete(_ context.Context, tenantID, id int64) error {
	if f.err != nil {
		return f.err
	}
	f.deletedID = id
	f.deletedTenant = tenantID
	return nil
}

func (f *fakeCameraStore) TouchHeartbeat(_ context.Context, deviceID string, _ time.Time) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.heartbeatOf = deviceID
	return f.matched, nil
}

func (f *fakeCameraStore) MarkOffline(_ context.Context, deadline time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.offlineDeadlin = deadline
	return f.offlineN, nil
}

var _ model.CameraModel = (*fakeCameraStore)(nil)

func newCtx(store *fakeCameraStore, stream config.StreamConf) *svc.ServiceContext {
	return &svc.ServiceContext{Config: config.Config{Stream: stream}, Cameras: store}
}

func ctxWithTenant(tenantID int64) context.Context {
	return ctxdata.SetTenantId(context.Background(), tenantID)
}

const validRtsp = "rtsp://192.168.1.50:554/stream1"

// ---------------------------------------------------------------------------
// #49 添加摄像头
// ---------------------------------------------------------------------------

func TestAddCamera_Success(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewAddCameraLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))

	resp, err := l.AddCamera(&types.AddCameraReq{
		Name: "1号厂房西北角", DeviceId: "cam-2001", AreaId: 12,
		RtspUrl: validRtsp, Status: model.CameraStatusOnline,
		Location: &types.Location{Lng: 113.12, Lat: 23.03, Floor: "3F"},
	})
	if err != nil {
		t.Fatalf("添加应成功: %v", err)
	}
	if resp.Id != 2001 {
		t.Errorf("应返回新建ID, 实际 %d", resp.Id)
	}
	if store.created.TenantID != 2 {
		t.Errorf("缺少租户隔离: %+v", store.created)
	}
	if store.created.Location == nil {
		t.Error("位置信息应序列化写入")
	}
}

// TestAddCamera_RtspInvalid RTSP 地址格式必须在创建期拦截(#49 的 M3-E-3002 → 400).
func TestAddCamera_RtspInvalid(t *testing.T) {
	cases := map[string]string{
		"http 地址":  "http://192.168.1.50/stream1",
		"缺协议":      "192.168.1.50:554/stream1",
		"空地址":      "",
		"仅有前缀":     "rtsp://",
		"rtmp 地址":  "rtmp://192.168.1.50/live",
	}
	for name, url := range cases {
		store := &fakeCameraStore{}
		l := NewAddCameraLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))
		_, err := l.AddCamera(&types.AddCameraReq{Name: "x", DeviceId: "cam-1", RtspUrl: url})
		if err == nil {
			t.Errorf("%s: 应被拒绝", name)
			continue
		}
		ce, ok := err.(*errorx.CodeError)
		if !ok || ce.Code != errorx.ErrVideoParamInvalid {
			t.Errorf("%s: 期望 %s, 实际 %v", name, errorx.ErrVideoParamInvalid, err)
		}
		if store.created != nil {
			t.Errorf("%s: 非法地址不应落库", name)
		}
	}
}

// TestAddCamera_RtspsAccepted 加密 RTSP 同样合法.
func TestAddCamera_RtspsAccepted(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewAddCameraLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))
	if _, err := l.AddCamera(&types.AddCameraReq{
		Name: "x", DeviceId: "cam-2", RtspUrl: "rtsps://cam.example.com/stream",
	}); err != nil {
		t.Errorf("rtsps 地址应被接受: %v", err)
	}
}

// TestAddCamera_ParamInvalid 缺租户/名称/设备ID 与状态越界.
func TestAddCamera_ParamInvalid(t *testing.T) {
	cases := map[string]types.AddCameraReq{
		"缺少租户":  {Name: "x", DeviceId: "cam-1", RtspUrl: validRtsp},
		"名称为空":  {DeviceId: "cam-1", RtspUrl: validRtsp},
		"设备ID空": {Name: "x", RtspUrl: validRtsp},
		"状态越界":  {Name: "x", DeviceId: "cam-1", RtspUrl: validRtsp, Status: 9},
	}
	for name, req := range cases {
		tenantID := int64(2)
		if name == "缺少租户" {
			tenantID = 0
		}
		store := &fakeCameraStore{}
		l := NewAddCameraLogic(ctxWithTenant(tenantID), newCtx(store, config.StreamConf{}))
		if _, err := l.AddCamera(&req); err == nil {
			t.Errorf("%s: 应返回错误", name)
		}
		if store.created != nil {
			t.Errorf("%s: 不应落库", name)
		}
	}
}

// TestAddCamera_DuplicateDevice 设备重复登记需转成可读的业务错误.
func TestAddCamera_DuplicateDevice(t *testing.T) {
	l := NewAddCameraLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: model.ErrCameraDuplicate}, config.StreamConf{}))
	_, err := l.AddCamera(&types.AddCameraReq{Name: "x", DeviceId: "cam-1", RtspUrl: validRtsp})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoCameraCreate {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoCameraCreate, err)
	}
}

// TestAddCamera_StoreError 其它数据库故障按添加失败返回.
func TestAddCamera_StoreError(t *testing.T) {
	l := NewAddCameraLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: errors.New("mysql down")}, config.StreamConf{}))
	if _, err := l.AddCamera(&types.AddCameraReq{Name: "x", DeviceId: "cam-1", RtspUrl: validRtsp}); err == nil {
		t.Error("存储故障应返回错误")
	}
}

// ---------------------------------------------------------------------------
// #50 列表
// ---------------------------------------------------------------------------

func TestListCameras_Success(t *testing.T) {
	now := time.Now()
	heartbeat := now.Add(-time.Minute)
	store := &fakeCameraStore{
		list: []*model.Camera{
			{ID: 2, TenantID: 2, Name: "B", DeviceID: "cam-2", Status: model.CameraStatusOnline, LastHeartbeatAt: &heartbeat, CreatedAt: now},
			{ID: 1, TenantID: 2, Name: "A", DeviceID: "cam-1", Status: model.CameraStatusOffline, CreatedAt: now},
		},
		total: 2,
	}
	l := NewListCamerasLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))
	resp, err := l.ListCameras(&types.ListCamerasReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 2 {
		t.Fatalf("分页响应异常: %+v", resp)
	}
	if resp.List[0].Id != 2 {
		t.Errorf("应按 id DESC 返回: %+v", resp.List[0])
	}
	if resp.List[0].LastHeartbeatAt != heartbeat.Unix() {
		t.Errorf("心跳时间未回传: %d", resp.List[0].LastHeartbeatAt)
	}
	if resp.List[1].LastHeartbeatAt != 0 {
		t.Errorf("无心跳应为 0, 实际 %d", resp.List[1].LastHeartbeatAt)
	}
}

// TestListCameras_StatusFilter status=0(离线)必须可筛选, 负数表示不筛选.
func TestListCameras_StatusFilter(t *testing.T) {
	store := &fakeCameraStore{}
	l := NewListCamerasLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))

	if _, err := l.ListCameras(&types.ListCamerasReq{Status: model.CameraStatusOffline, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=0 应可筛选: %v", err)
	}
	if _, err := l.ListCameras(&types.ListCamerasReq{Status: -1, Page: 1, PageSize: 10}); err != nil {
		t.Errorf("status=-1 应表示不筛选: %v", err)
	}
	if _, err := l.ListCameras(&types.ListCamerasReq{Status: 5, Page: 1, PageSize: 10}); err == nil {
		t.Error("status 越界应报错")
	}
}

// ---------------------------------------------------------------------------
// #51 取流
// ---------------------------------------------------------------------------

func TestGetStream_Success(t *testing.T) {
	store := &fakeCameraStore{detail: &model.Camera{
		ID: 2001, TenantID: 2, DeviceID: "cam-2001", RtspURL: validRtsp, Status: model.CameraStatusOnline,
	}}
	stream := config.StreamConf{FlvBaseURL: "http://media/live/", ExpiresSeconds: 600}
	l := NewGetStreamLogic(ctxWithTenant(2), newCtx(store, stream))

	resp, err := l.GetStream(&types.IdReq{Id: 2001})
	if err != nil {
		t.Fatalf("取流应成功: %v", err)
	}
	if resp.RtspUrl != validRtsp {
		t.Errorf("RTSP 地址不一致: %s", resp.RtspUrl)
	}
	if resp.FlvUrl != "http://media/live/cam-2001.flv" {
		t.Errorf("FLV 地址拼接异常: %s", resp.FlvUrl)
	}
	if resp.ExpiresAt <= time.Now().Unix() {
		t.Errorf("有效期应在未来: %d", resp.ExpiresAt)
	}
}

// TestGetStream_NoFlvBase 未配置流媒体服务时 FLV 应为空, 不编造地址.
func TestGetStream_NoFlvBase(t *testing.T) {
	store := &fakeCameraStore{detail: &model.Camera{ID: 1, DeviceID: "cam-1", Status: model.CameraStatusOnline, RtspURL: validRtsp}}
	l := NewGetStreamLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))

	resp, err := l.GetStream(&types.IdReq{Id: 1})
	if err != nil {
		t.Fatalf("取流应成功: %v", err)
	}
	if resp.FlvUrl != "" {
		t.Errorf("未配置基地址时 FLV 应为空, 实际 %s", resp.FlvUrl)
	}
	if resp.ExpiresAt == 0 {
		t.Error("未配置有效期时应给默认值")
	}
}

// TestGetStream_Offline 离线/故障设备不下发地址(#51 的 M3-E-3005).
func TestGetStream_Offline(t *testing.T) {
	for _, status := range []int8{model.CameraStatusOffline, model.CameraStatusFault} {
		store := &fakeCameraStore{detail: &model.Camera{ID: 1, DeviceID: "cam-1", Status: status, RtspURL: validRtsp}}
		l := NewGetStreamLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))
		_, err := l.GetStream(&types.IdReq{Id: 1})
		ce, ok := err.(*errorx.CodeError)
		if !ok || ce.Code != errorx.ErrVideoCameraOffline {
			t.Errorf("status=%d 期望 %s, 实际 %v", status, errorx.ErrVideoCameraOffline, err)
		}
	}
}

// TestGetStream_NotFound 摄像头不存在与存储故障的分支.
func TestGetStream_NotFound(t *testing.T) {
	l := NewGetStreamLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: model.ErrCameraNotFound}, config.StreamConf{}))
	_, err := l.GetStream(&types.IdReq{Id: 404})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrVideoStreamNotFound {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrVideoStreamNotFound, err)
	}

	broken := NewGetStreamLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{err: errors.New("mysql down")}, config.StreamConf{}))
	if _, err := broken.GetStream(&types.IdReq{Id: 1}); err == nil {
		t.Error("存储故障应返回错误")
	}
}

// TestGetStream_ParamInvalid 缺租户与非法ID.
func TestGetStream_ParamInvalid(t *testing.T) {
	if _, err := NewGetStreamLogic(ctxWithTenant(0), newCtx(&fakeCameraStore{}, config.StreamConf{})).
		GetStream(&types.IdReq{Id: 1}); err == nil {
		t.Error("缺少租户应报错")
	}
	if _, err := NewGetStreamLogic(ctxWithTenant(2), newCtx(&fakeCameraStore{}, config.StreamConf{})).
		GetStream(&types.IdReq{Id: 0}); err == nil {
		t.Error("ID 非法应报错")
	}
}

// TestGetStream_Signed 配置 SignSecret 后必须真正下发签名, 且签名可被离线校验.
func TestGetStream_Signed(t *testing.T) {
	const secret = "stream-sign-test-secret"
	store := &fakeCameraStore{detail: &model.Camera{
		ID: 2001, TenantID: 2, DeviceID: "cam-2001", RtspURL: validRtsp, Status: model.CameraStatusOnline,
	}}
	stream := config.StreamConf{FlvBaseURL: "http://media/live", ExpiresSeconds: 600, SignSecret: secret}
	l := NewGetStreamLogic(ctxWithTenant(2), newCtx(store, stream))

	resp, err := l.GetStream(&types.IdReq{Id: 2001})
	if err != nil {
		t.Fatalf("取流应成功: %v", err)
	}
	if resp.Sign == "" {
		t.Fatal("配置 SignSecret 后必须下发签名")
	}
	if resp.SignAlg != SignAlgorithm {
		t.Errorf("签名算法标识应为 %s, 实际 %q", SignAlgorithm, resp.SignAlg)
	}
	if !VerifyStreamSign(secret, resp.CameraId, resp.ExpiresAt, resp.Sign, time.Now()) {
		t.Error("下发的签名应可通过校验")
	}
	// FLV 是 HTTP 拉流, 签名必须体现在 URL 上, 否则流媒体侧无从校验.
	if !strings.Contains(resp.FlvUrl, QuerySign+"=") {
		t.Errorf("FLV 地址应带签名参数, 实际 %s", resp.FlvUrl)
	}
	// RTSP 不下发签名参数: 部分设备不接受带 query 的 rtsp:// 地址(鉴权改用 sign 字段).
	if strings.Contains(resp.RtspUrl, QuerySign+"=") {
		t.Errorf("RTSP 地址不应追加签名参数, 实际 %s", resp.RtspUrl)
	}
}

// TestGetStream_UnsignedKeepsLegacyBehaviour 未配置密钥时保持上线前行为:
// 空签名 + FLV 不带参数, 避免本地联调/未启用签名的部署被迫改造.
func TestGetStream_UnsignedKeepsLegacyBehaviour(t *testing.T) {
	store := &fakeCameraStore{detail: &model.Camera{
		ID: 2001, TenantID: 2, DeviceID: "cam-2001", RtspURL: validRtsp, Status: model.CameraStatusOnline,
	}}
	stream := config.StreamConf{FlvBaseURL: "http://media/live", ExpiresSeconds: 600}
	l := NewGetStreamLogic(ctxWithTenant(2), newCtx(store, stream))

	resp, err := l.GetStream(&types.IdReq{Id: 2001})
	if err != nil {
		t.Fatalf("取流应成功: %v", err)
	}
	if resp.Sign != "" || resp.SignAlg != "" {
		t.Errorf("未启用签名时 sign/sign_alg 应为空, 实际 %q/%q", resp.Sign, resp.SignAlg)
	}
	if resp.FlvUrl != "http://media/live/cam-2001.flv" {
		t.Errorf("未启用签名时 FLV 地址不应带参数, 实际 %s", resp.FlvUrl)
	}
}

// ---------------------------------------------------------------------------
// #50 在线状态 Redis 缓存
// ---------------------------------------------------------------------------

// fakeStatusCache 在线状态缓存替身, 用内存 map 表达"哪些设备在离线阈值内上报过心跳".
type fakeStatusCache struct {
	online map[string]time.Time
}

func (f *fakeStatusCache) Mark(_ context.Context, deviceID string, at time.Time) error {
	if f.online == nil {
		f.online = map[string]time.Time{}
	}
	f.online[deviceID] = at
	return nil
}

func (f *fakeStatusCache) Online(_ context.Context, deviceID string) (bool, time.Time) {
	at, ok := f.online[deviceID]
	return ok, at
}

var _ svc.StatusCacheStore = (*fakeStatusCache)(nil)

// TestListCameras_CacheOverridesStaleOffline 缓存命中在线时, 列表应立即显示在线,
// 不必等离线扫描(默认 60s)把 MySQL 的 status 改回来.
func TestListCameras_CacheOverridesStaleOffline(t *testing.T) {
	now := time.Now()
	store := &fakeCameraStore{
		list: []*model.Camera{
			{ID: 2, TenantID: 2, Name: "B", DeviceID: "cam-2", Status: model.CameraStatusOffline, CreatedAt: now},
			{ID: 1, TenantID: 2, Name: "A", DeviceID: "cam-1", Status: model.CameraStatusOffline, CreatedAt: now},
		},
		total: 2,
	}
	svcCtx := newCtx(store, config.StreamConf{})
	svcCtx.StatusCache = &fakeStatusCache{online: map[string]time.Time{"cam-2": now}}
	l := NewListCamerasLogic(ctxWithTenant(2), svcCtx)

	resp, err := l.ListCameras(&types.ListCamerasReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.List[0].Status != model.CameraStatusOnline {
		t.Errorf("cam-2 缓存命中应显示在线, 实际 status=%d", resp.List[0].Status)
	}
	if resp.List[1].Status != model.CameraStatusOffline {
		t.Errorf("cam-1 无缓存应保持落库状态离线, 实际 status=%d", resp.List[1].Status)
	}
}

// TestListCameras_FaultNotOverriddenByCache 故障态是人工排障结论,
// 不能被一次心跳静默洗成在线(与 MarkOffline 跳过故障行的口径一致).
func TestListCameras_FaultNotOverriddenByCache(t *testing.T) {
	now := time.Now()
	store := &fakeCameraStore{
		list: []*model.Camera{
			{ID: 1, TenantID: 2, Name: "A", DeviceID: "cam-1", Status: model.CameraStatusFault, CreatedAt: now},
		},
		total: 1,
	}
	svcCtx := newCtx(store, config.StreamConf{})
	svcCtx.StatusCache = &fakeStatusCache{online: map[string]time.Time{"cam-1": now}}
	l := NewListCamerasLogic(ctxWithTenant(2), svcCtx)

	resp, err := l.ListCameras(&types.ListCamerasReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.List[0].Status != model.CameraStatusFault {
		t.Errorf("故障态不应被心跳缓存覆盖, 实际 status=%d", resp.List[0].Status)
	}
}

// TestListCameras_NoCacheFallsBackToMySQL 未接入缓存时行为必须与改造前一致.
func TestListCameras_NoCacheFallsBackToMySQL(t *testing.T) {
	now := time.Now()
	store := &fakeCameraStore{
		list: []*model.Camera{
			{ID: 1, TenantID: 2, Name: "A", DeviceID: "cam-1", Status: model.CameraStatusOffline, CreatedAt: now},
		},
		total: 1,
	}
	l := NewListCamerasLogic(ctxWithTenant(2), newCtx(store, config.StreamConf{}))

	resp, err := l.ListCameras(&types.ListCamerasReq{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.List[0].Status != model.CameraStatusOffline {
		t.Errorf("无缓存时应回退 MySQL 状态, 实际 status=%d", resp.List[0].Status)
	}
}
