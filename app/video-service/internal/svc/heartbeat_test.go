package svc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"

	kafkago "github.com/segmentio/kafka-go"
)

// fakeCameras 心跳链路替身: 只记录被登记心跳的设备ID.
type fakeCameras struct {
	touched []string
	matched int
	err     error
}

func (f *fakeCameras) Create(context.Context, *model.Camera) error { return nil }
func (f *fakeCameras) FindByID(context.Context, int64, int64) (*model.Camera, error) {
	return nil, model.ErrCameraNotFound
}
func (f *fakeCameras) Update(context.Context, int64, int64, model.CameraPatch) error { return nil }
func (f *fakeCameras) Delete(context.Context, int64, int64) error                    { return nil }
func (f *fakeCameras) List(context.Context, model.CameraListFilter) ([]*model.Camera, int64, error) {
	return nil, 0, nil
}
func (f *fakeCameras) MarkOffline(context.Context, time.Time) (int64, error) { return 0, nil }

func (f *fakeCameras) TouchHeartbeat(_ context.Context, deviceID string, _ time.Time) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.touched = append(f.touched, deviceID)
	return f.matched, nil
}

var _ model.CameraModel = (*fakeCameras)(nil)

func heartbeatCtx(cameras model.CameraModel, types []string) *ServiceContext {
	return &ServiceContext{
		Config:  config.Config{Heartbeat: config.HeartbeatConf{DeviceTypes: types}},
		Cameras: cameras,
	}
}

func msg(t *testing.T, m deviceMessage) kafkago.Message {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return kafkago.Message{Value: b}
}

// TestHandleDeviceMessage_DeviceTypeGate 非白名单设备类型不登记心跳:
// 地磁/门禁的遥测也走同一条 topic, 不过滤会让每台非摄像头设备都白查一次库.
func TestHandleDeviceMessage_DeviceTypeGate(t *testing.T) {
	store := &fakeCameras{}
	s := heartbeatCtx(store, []string{"camera"})

	if err := s.handleDeviceMessage(context.Background(),
		msg(t, deviceMessage{DeviceID: "cam-1", DeviceType: "camera"})); err != nil {
		t.Fatalf("摄像头心跳应被处理: %v", err)
	}
	if err := s.handleDeviceMessage(context.Background(),
		msg(t, deviceMessage{DeviceID: "magnet-1", DeviceType: "magnetometer"})); err != nil {
		t.Fatalf("非摄像头消息应直接跳过: %v", err)
	}
	if len(store.touched) != 1 || store.touched[0] != "cam-1" {
		t.Errorf("只有摄像头应登记心跳, 实际 %v", store.touched)
	}
}

// TestHandleDeviceMessage_Malformed 坏消息跳过但返回 nil: 没有死信台账时,
// 返回 error 会让单条坏消息卡死整个分区(毒丸), 比丢一条心跳严重.
func TestHandleDeviceMessage_Malformed(t *testing.T) {
	store := &fakeCameras{}
	s := heartbeatCtx(store, []string{"camera"})
	if err := s.handleDeviceMessage(context.Background(), kafkago.Message{Value: []byte("{not json")}); err != nil {
		t.Errorf("坏消息应跳过而非卡分区: %v", err)
	}
	if len(store.touched) != 0 {
		t.Error("坏消息不应登记心跳")
	}
}

// TestOnHeartbeat_StorageError 存储故障必须上抛(消费端据此重投), 不能当作"没命中"吞掉.
func TestOnHeartbeat_StorageError(t *testing.T) {
	store := &fakeCameras{err: context.DeadlineExceeded}
	s := heartbeatCtx(store, []string{"camera"})
	if err := s.onHeartbeat(context.Background(), "cam-1"); err == nil {
		t.Error("存储故障应返回错误以触发重投")
	}
}

// TestHeartbeatConf_DeviceTypeAllowed 未配置白名单时按默认 camera 兜底.
func TestHeartbeatConf_DeviceTypeAllowed(t *testing.T) {
	var c config.HeartbeatConf
	if !c.DeviceTypeAllowed("camera") {
		t.Error("未配置白名单时应按默认 camera 处理")
	}
	if c.DeviceTypeAllowed("magnetometer") {
		t.Error("非摄像头类型应被排除")
	}
}

// ---------------------------------------------------------------------------
// 在线状态缓存写入(#50 Redis 缓存)
// ---------------------------------------------------------------------------

// fakeStatusCache 记录 Mark 调用, 用于验证心跳是否刷新在线缓存.
type fakeStatusCache struct {
	marked map[string]time.Time
	err    error
}

func (f *fakeStatusCache) Mark(_ context.Context, deviceID string, at time.Time) error {
	if f.err != nil {
		return f.err
	}
	if f.marked == nil {
		f.marked = map[string]time.Time{}
	}
	f.marked[deviceID] = at
	return nil
}

func (f *fakeStatusCache) Online(context.Context, string) (bool, time.Time) {
	return false, time.Time{}
}

var _ StatusCacheStore = (*fakeStatusCache)(nil)

func heartbeatCtxWithCache(cameras model.CameraModel, cache StatusCacheStore) *ServiceContext {
	return &ServiceContext{
		Config:      config.Config{Heartbeat: config.HeartbeatConf{DeviceTypes: []string{"camera"}}},
		Cameras:     cameras,
		StatusCache: cache,
	}
}

// TestOnHeartbeat_WritesCache 唯一命中的心跳必须刷新在线缓存,
// 否则列表接口仍要等最长 60s 的离线扫描才能看到设备上线.
func TestOnHeartbeat_WritesCache(t *testing.T) {
	cache := &fakeStatusCache{}
	s := heartbeatCtxWithCache(&fakeCameras{matched: 1}, cache)

	if err := s.onHeartbeat(context.Background(), "cam-1"); err != nil {
		t.Fatalf("心跳登记应成功: %v", err)
	}
	if _, ok := cache.marked["cam-1"]; !ok {
		t.Errorf("命中的心跳应写入在线缓存, 实际 %v", cache.marked)
	}
}

// TestOnHeartbeat_NoCacheWriteWhenUnmatched device_id 未登记或跨租户重名时不能写缓存:
// 前者是地址簿漏登记, 后者无法归属园区 —— 写任何一个都会把在线状态安到错误的对象上.
func TestOnHeartbeat_NoCacheWriteWhenUnmatched(t *testing.T) {
	for _, matched := range []int{0, 2} {
		cache := &fakeStatusCache{}
		s := heartbeatCtxWithCache(&fakeCameras{matched: matched}, cache)
		if err := s.onHeartbeat(context.Background(), "cam-1"); err != nil {
			t.Fatalf("matched=%d 不应返回错误: %v", matched, err)
		}
		if len(cache.marked) != 0 {
			t.Errorf("matched=%d 不应写入缓存, 实际 %v", matched, cache.marked)
		}
	}
}

// TestOnHeartbeat_CacheFailureIsNotFatal 缓存写失败不能回错:
// 缓存只是加速层, MSQL 侧状态已落库; 若因此重投心跳, Redis 抖动会卡死整个分区.
func TestOnHeartbeat_CacheFailureIsNotFatal(t *testing.T) {
	s := heartbeatCtxWithCache(&fakeCameras{matched: 1}, &fakeStatusCache{err: context.DeadlineExceeded})
	if err := s.onHeartbeat(context.Background(), "cam-1"); err != nil {
		t.Errorf("缓存故障不应导致心跳消费失败: %v", err)
	}
}

// TestOnHeartbeat_NilCacheIsSafe 未接入缓存(本地/未配置 Redis)时链路必须照常工作.
func TestOnHeartbeat_NilCacheIsSafe(t *testing.T) {
	s := heartbeatCtxWithCache(&fakeCameras{matched: 1}, nil)
	if err := s.onHeartbeat(context.Background(), "cam-1"); err != nil {
		t.Errorf("未接入缓存时应正常登记心跳: %v", err)
	}
}
