package logic

import (
	"context"
	"strconv"
	"strings"
	"time"

	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// defaultMaxRangeHours 单次回放查询的默认最大跨度(docs 支持更长 Histories 时应在配置里显式放开).
const defaultMaxRangeHours = 24

// PlaybackLogic 回放查询: 按录像计划推导 [start,end) 内的可用录像窗口并签发回放地址.
//
// 本服务不存录像文件(见 docs/m3/11), 因此"可回放"的判定只建立在两件事上:
//  1. 计划是否覆盖该时段(scheduled 的日/时段, always 的全天);
//  2. 该时段是否仍在保留期内、且晚于计划创建时间。
// 二者都不满足时不返回窗口 —— 宁可少给, 也不把没录过的时段说成有录像.
type PlaybackLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPlaybackLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PlaybackLogic {
	return &PlaybackLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *PlaybackLogic) Playback(req *types.PlaybackReq) (*types.PlaybackResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.CameraId <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoRecordParamInvalid, "camera_id 非法")
	}
	if l.svcCtx.Cameras == nil || l.svcCtx.RecordPlans == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "摄像头/录像计划存储未就绪(MySQL 未配置)")
	}
	if req.StartTime <= 0 || req.EndTime <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoRecordParamInvalid, "start_time / end_time 需为正的秒级时间戳")
	}
	if req.EndTime <= req.StartTime {
		return nil, errorx.NewError(errorx.ErrVideoRecordParamInvalid, "end_time 必须大于 start_time")
	}

	now := time.Now()
	start, end := time.Unix(req.StartTime, 0), time.Unix(req.EndTime, 0)

	maxRange := l.svcCtx.Config.Record.MaxRangeHours
	if maxRange <= 0 {
		maxRange = defaultMaxRangeHours
	}
	if end.Sub(start) > time.Duration(maxRange)*time.Hour {
		return nil, errorx.NewError(errorx.ErrVideoRecordParamInvalid,
			"回放区间超出上限 "+strconv.Itoa(maxRange)+" 小时")
	}
	// 录像不可能来自未来: 请求区间的上界裁剪到当前时刻。
	// 不裁剪时, 一条全天计划会把"今天剩下的时间"也算成可回放窗口,
	// 前端据此渲染出的进度条会指向还不存在的内容。
	if end.After(now) {
		end = now
	}
	if !end.After(start) {
		return nil, errorx.NewError(errorx.ErrVideoRecordParamInvalid, "start_time 不能晚于当前时间")
	}

	camera, err := l.svcCtx.Cameras.FindByID(l.ctx, tenantID, req.CameraId)
	if err != nil {
		if err == model.ErrCameraNotFound {
			return nil, errorx.NewError(errorx.ErrVideoCameraNotFound, "摄像头不存在")
		}
		l.Errorf("find camera failed id=%d: %v", req.CameraId, err)
		return nil, errorx.NewError(errorx.ErrVideoPlayback, "查询摄像头失败")
	}
	// 离线/故障摄像头**不阻断**回放: 查的是历史录像(媒体在网关侧),
	// 设备当下是否在线只影响实况拉流(#51)。把两者混为一谈会让"设备坏了但要看证据"这条路走不通.

	plans, err := l.svcCtx.RecordPlans.ListEnabledByCamera(l.ctx, tenantID, req.CameraId)
	if err != nil {
		l.Errorf("list enabled record plans failed camera_id=%d: %v", req.CameraId, err)
		return nil, errorx.NewError(errorx.ErrVideoPlayback, "查询录像计划失败")
	}

	windows := availableWindows(plans, start, end, now)
	expiresAt := now.Add(l.playbackTTL()).Unix()
	secret := l.svcCtx.Config.Stream.SignSecret

	segments := make([]types.PlaybackSegment, 0, len(windows))
	for _, w := range windows {
		startUnix, endUnix := w.start.Unix(), w.end.Unix()
		sign := SignPlayback(secret, camera.ID, startUnix, endUnix, expiresAt)
		alg := ""
		if sign != "" {
			alg = SignAlgorithm
		}
		segments = append(segments, types.PlaybackSegment{
			PlanId:      w.planID,
			PlanName:    w.planName,
			StartTime:   startUnix,
			EndTime:     endUnix,
			PlaybackUrl: playbackURL(l.svcCtx.Config.Record.PlaybackBaseURL, camera.DeviceID, startUnix, endUnix, sign, expiresAt),
			ExpiresAt:   expiresAt,
			Sign:        sign,
			SignAlg:     alg,
		})
	}

	return &types.PlaybackResp{
		CameraId:  camera.ID,
		StartTime: start.Unix(),
		EndTime:   end.Unix(),
		HasPlan:   len(plans) > 0,
		Total:     int64(len(segments)),
		List:      segments,
	}, nil
}

// playbackTTL 回放地址有效期: Record.ExpiresSeconds → Stream.ExpiresSeconds → 3600.
// 与实况共用兜底口径, 避免"实况 1 小时、回放 1 分钟"这种没有理由的不一致。
func (l *PlaybackLogic) playbackTTL() time.Duration {
	if n := l.svcCtx.Config.Record.ExpiresSeconds; n > 0 {
		return time.Duration(n) * time.Second
	}
	if n := l.svcCtx.Config.Stream.ExpiresSeconds; n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Hour
}

// playbackURL 拼接回放地址: {base}/{deviceID}.mp4?start=&end=[&expires=&sign=].
//
// base 为空返回空串: 没有实际承载点时不下发编造出来的 URL(与 #51 FLV 的取舍一致),
// 时间窗口仍然返回 —— 调用方至少能知道"按计划哪些时段有录像"。
// 未启用签名时不追加 expires/sign, 行为与签名能力上线前一致。
func playbackURL(base, deviceID string, startUnix, endUnix int64, sign string, expiresAt int64) string {
	base = strings.TrimSpace(base)
	if base == "" || deviceID == "" {
		return ""
	}
	url := strings.TrimSuffix(base, "/") + "/" + deviceID + ".mp4" +
		"?start=" + strconv.FormatInt(startUnix, 10) +
		"&end=" + strconv.FormatInt(endUnix, 10)
	if sign == "" {
		return url
	}
	return url + "&" + QueryExpires + "=" + strconv.FormatInt(expiresAt, 10) +
		"&" + QuerySign + "=" + sign
}
