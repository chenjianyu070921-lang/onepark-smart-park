package logic

import (
	"errors"
	"strings"
	"testing"
	"time"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/types"
	"onepark/common/errorx"
)

// TestPlayback_NoPlanReturnsEmptyButFlagged 没有录像计划时返回空列表且 has_plan=false:
// 调用方据此区分"没配计划"与"配了但这段没录上", 否则两者都表现为"回放不了".
func TestPlayback_NoPlanReturnsEmptyButFlagged(t *testing.T) {
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	plans := &fakeRecordPlanStore{} // list 为 nil → 无启用计划
	l := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	resp, err := l.Playback(&types.PlaybackReq{
		CameraId: 2001, StartTime: now.Add(-time.Hour).Unix(), EndTime: now.Unix(),
	})
	if err != nil {
		t.Fatalf("回放查询应成功: %v", err)
	}
	if resp.HasPlan {
		t.Error("无启用计划时 has_plan 应为 false")
	}
	if resp.Total != 0 || len(resp.List) != 0 {
		t.Errorf("无计划时不应返回窗口: %+v", resp)
	}
}

// TestPlayback_WithPlanReturnsSignedWindows 有计划时返回窗口, 且签名可被离线校验.
func TestPlayback_WithPlanReturnsSignedWindows(t *testing.T) {
	const secret = "playback-sign-secret"
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	plans := &fakeRecordPlanStore{list: []*model.RecordPlan{alwaysPlan(11)}}

	svcCtx := newRecordCtx(cameras, plans, config.RecordConf{
		PlaybackBaseURL: "http://media/record", ExpiresSeconds: 600,
	})
	svcCtx.Config.Stream.SignSecret = secret
	l := NewPlaybackLogic(ctxWithTenant(2), svcCtx)

	start := now.Add(-time.Hour).Unix()
	resp, err := l.Playback(&types.PlaybackReq{CameraId: 2001, StartTime: start, EndTime: now.Unix()})
	if err != nil {
		t.Fatalf("回放查询应成功: %v", err)
	}
	if !resp.HasPlan || resp.Total != 1 {
		t.Fatalf("应返回 1 段窗口: %+v", resp)
	}
	seg := resp.List[0]
	if seg.StartTime != start || seg.EndTime != now.Unix() {
		t.Errorf("窗口边界应与请求一致: [%d,%d)", seg.StartTime, seg.EndTime)
	}
	if seg.Sign == "" || seg.SignAlg != SignAlgorithm {
		t.Errorf("配置了密钥必须下发签名: sign=%q alg=%q", seg.Sign, seg.SignAlg)
	}
	if !VerifyPlaybackSign(secret, 2001, seg.StartTime, seg.EndTime, seg.ExpiresAt, seg.Sign, now) {
		t.Error("下发的回放签名应可通过校验")
	}
	if !strings.Contains(seg.PlaybackUrl, "http://media/record/cam-2001.mp4?start=") {
		t.Errorf("回放地址拼接异常: %s", seg.PlaybackUrl)
	}
	if !strings.Contains(seg.PlaybackUrl, QuerySign+"=") {
		t.Errorf("回放地址应带签名参数: %s", seg.PlaybackUrl)
	}
}

// TestPlayback_SignatureBindsTimeRange 签名必须绑定时间范围与摄像头:
// 否则一个合法签名可被挪到同一摄像头的任意时段上重放, 时段授权形同虚设.
func TestPlayback_SignatureBindsTimeRange(t *testing.T) {
	const secret = "playback-sign-secret"
	now := time.Now()
	expires := now.Add(time.Hour).Unix()

	sign := SignPlayback(secret, 1, 100, 200, expires)
	if SignPlayback(secret, 1, 300, 400, expires) == sign {
		t.Error("不同时间范围必须产生不同签名")
	}
	if SignPlayback(secret, 2, 100, 200, expires) == sign {
		t.Error("不同摄像头必须产生不同签名")
	}
	if SignPlayback("", 1, 100, 200, expires) != "" {
		t.Error("未配置密钥时不应签名")
	}
	if !VerifyPlaybackSign(secret, 1, 100, 200, expires, sign, now) {
		t.Error("正确签名应通过校验")
	}
	if VerifyPlaybackSign(secret, 1, 100, 200, expires, sign, now.Add(2*time.Hour)) {
		t.Error("过期签名必须被拒绝")
	}
}

// TestPlayback_OfflineCameraStillPlayable 摄像头离线不阻断历史回放:
// 查的是网关侧已录文件, 设备当前是否在线只影响实况拉流(#51).
func TestPlayback_OfflineCameraStillPlayable(t *testing.T) {
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{
		ID: 2001, TenantID: 2, DeviceID: "cam-2001", Status: model.CameraStatusOffline,
	}}
	plans := &fakeRecordPlanStore{list: []*model.RecordPlan{alwaysPlan(11)}}
	l := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	resp, err := l.Playback(&types.PlaybackReq{
		CameraId: 2001, StartTime: now.Add(-time.Hour).Unix(), EndTime: now.Unix(),
	})
	if err != nil {
		t.Fatalf("离线设备也应能回放历史录像: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("应返回 1 段窗口, 实际 %d", resp.Total)
	}
}

// TestPlayback_TrimsFutureRange 请求区间超出当前时刻时上界被裁剪:
// 不裁剪会把"今天还没到的时间"也算成可回放窗口.
func TestPlayback_TrimsFutureRange(t *testing.T) {
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	plans := &fakeRecordPlanStore{list: []*model.RecordPlan{alwaysPlan(11)}}
	l := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	resp, err := l.Playback(&types.PlaybackReq{
		CameraId: 2001, StartTime: now.Add(-time.Hour).Unix(), EndTime: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("回放查询应成功: %v", err)
	}
	if len(resp.List) != 1 {
		t.Fatalf("应返回 1 段窗口, 实际 %d", len(resp.List))
	}
	if resp.List[0].EndTime > now.Unix()+1 {
		t.Errorf("窗口上界应裁剪到当前时刻: end=%d now=%d", resp.List[0].EndTime, now.Unix())
	}
}

// TestPlayback_ParamInvalid 各类非法入参统一落到 400 的错误码.
func TestPlayback_ParamInvalid(t *testing.T) {
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	newLogic := func(tenant int64, conf config.RecordConf) *PlaybackLogic {
		return NewPlaybackLogic(ctxWithTenant(tenant), newRecordCtx(cameras, &fakeRecordPlanStore{}, conf))
	}

	cases := map[string]struct {
		tenant   int64
		conf     config.RecordConf
		req      types.PlaybackReq
		wantCode string
	}{
		"缺少租户": {
			tenant:   0,
			req:      types.PlaybackReq{CameraId: 1, StartTime: 1, EndTime: 2},
			wantCode: errorx.ErrVideoParamInvalid,
		},
		"摄像头ID非法": {
			tenant:   2,
			req:      types.PlaybackReq{StartTime: 1, EndTime: 2},
			wantCode: errorx.ErrVideoRecordParamInvalid,
		},
		"时间戳非正": {
			tenant:   2,
			req:      types.PlaybackReq{CameraId: 1, StartTime: 0, EndTime: 0},
			wantCode: errorx.ErrVideoRecordParamInvalid,
		},
		"区间倒置": {
			tenant:   2,
			req:      types.PlaybackReq{CameraId: 1, StartTime: now.Unix(), EndTime: now.Add(-time.Hour).Unix()},
			wantCode: errorx.ErrVideoRecordParamInvalid,
		},
		"跨度超限": {
			tenant: 2, conf: config.RecordConf{MaxRangeHours: 1},
			req:      types.PlaybackReq{CameraId: 1, StartTime: now.Add(-5 * time.Hour).Unix(), EndTime: now.Unix()},
			wantCode: errorx.ErrVideoRecordParamInvalid,
		},
		"起点在未来": {
			tenant:   2,
			req:      types.PlaybackReq{CameraId: 1, StartTime: now.Add(time.Hour).Unix(), EndTime: now.Add(2 * time.Hour).Unix()},
			wantCode: errorx.ErrVideoRecordParamInvalid,
		},
	}
	for name, c := range cases {
		_, err := newLogic(c.tenant, c.conf).Playback(&c.req)
		if err == nil {
			t.Errorf("%s: 应返回错误", name)
			continue
		}
		expectCodeErr(t, err, c.wantCode)
	}
}

// TestPlayback_NoBaseURLKeepsEmptyURL 未配置回放基地址时不下发编造的 URL,
// 但时间窗口仍然返回 —— 至少让调用方知道"哪些时段按计划有录像".
func TestPlayback_NoBaseURLKeepsEmptyURL(t *testing.T) {
	now := time.Now()
	cameras := &fakeCameraStore{detail: &model.Camera{ID: 2001, TenantID: 2, DeviceID: "cam-2001"}}
	plans := &fakeRecordPlanStore{list: []*model.RecordPlan{alwaysPlan(11)}}
	l := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(cameras, plans, config.RecordConf{}))

	resp, err := l.Playback(&types.PlaybackReq{
		CameraId: 2001, StartTime: now.Add(-time.Hour).Unix(), EndTime: now.Unix(),
	})
	if err != nil {
		t.Fatalf("回放查询应成功: %v", err)
	}
	if len(resp.List) != 1 {
		t.Fatalf("应仍返回窗口, 实际 %d", len(resp.List))
	}
	if resp.List[0].PlaybackUrl != "" {
		t.Errorf("未配置基地址时应返回空 URL, 实际 %s", resp.List[0].PlaybackUrl)
	}
	if resp.List[0].Sign != "" {
		t.Errorf("未配置密钥时不应下发签名, 实际 %s", resp.List[0].Sign)
	}
}

// TestPlayback_CameraNotFound 摄像头不存在 / 计划表查询失败的错误码区分.
func TestPlayback_CameraNotFound(t *testing.T) {
	now := time.Now()
	req := &types.PlaybackReq{CameraId: 404, StartTime: now.Add(-time.Hour).Unix(), EndTime: now.Unix()}

	notFound := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(
		&fakeCameraStore{err: model.ErrCameraNotFound}, &fakeRecordPlanStore{}, config.RecordConf{}))
	if _, err := notFound.Playback(req); err == nil {
		t.Fatal("期望返回错误")
	} else {
		expectCodeErr(t, err, errorx.ErrVideoCameraNotFound)
	}

	broken := NewPlaybackLogic(ctxWithTenant(2), newRecordCtx(
		&fakeCameraStore{detail: &model.Camera{ID: 404, TenantID: 2}},
		&fakeRecordPlanStore{err: errors.New("mysql down")}, config.RecordConf{}))
	if _, err := broken.Playback(req); err == nil {
		t.Fatal("期望返回错误")
	} else {
		expectCodeErr(t, err, errorx.ErrVideoPlayback)
	}
}

// TestPlaybackTTL_FallbackChain 回放有效期兜底链: Record → Stream → 1 小时.
func TestPlaybackTTL_FallbackChain(t *testing.T) {
	cameras := &fakeCameraStore{}

	svcCtx := newRecordCtx(cameras, &fakeRecordPlanStore{}, config.RecordConf{ExpiresSeconds: 60})
	if got := NewPlaybackLogic(ctxWithTenant(2), svcCtx).playbackTTL(); got != time.Minute {
		t.Errorf("应优先取 Record.ExpiresSeconds=60s, 实际 %v", got)
	}

	streamCtx := newRecordCtx(cameras, &fakeRecordPlanStore{}, config.RecordConf{ExpiresSeconds: 120})
	if got := NewPlaybackLogic(ctxWithTenant(2), streamCtx).playbackTTL(); got != 2*time.Minute {
		t.Errorf("Record 优先, 实际 %v", got)
	}

	fallback := newRecordCtx(cameras, &fakeRecordPlanStore{}, config.RecordConf{})
	fallback.Config.Stream.ExpiresSeconds = 120
	if got := NewPlaybackLogic(ctxWithTenant(2), fallback).playbackTTL(); got != 2*time.Minute {
		t.Errorf("Record 未配置时应退到 Stream.ExpiresSeconds=120s, 实际 %v", got)
	}

	zero := newRecordCtx(cameras, &fakeRecordPlanStore{}, config.RecordConf{})
	zero.Config.Stream.ExpiresSeconds = 0
	if got := NewPlaybackLogic(ctxWithTenant(2), zero).playbackTTL(); got != time.Hour {
		t.Errorf("均未配置时应退到 1 小时, 实际 %v", got)
	}
}
