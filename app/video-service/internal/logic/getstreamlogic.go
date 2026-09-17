package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// GetStreamLogic 获取视频流地址(docs/m3/04 #51).
// M3 只管理与下发流地址, 不实现转码/推流; flv 地址由独立流媒体服务提供.
type GetStreamLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetStreamLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetStreamLogic {
	return &GetStreamLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *GetStreamLogic) GetStream(req *types.IdReq) (*types.StreamResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "摄像头ID非法")
	}
	if l.svcCtx.Cameras == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "摄像头存储未就绪(MySQL 未配置)")
	}

	camera, err := l.svcCtx.Cameras.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrCameraNotFound {
			return nil, errorx.NewError(errorx.ErrVideoStreamNotFound, "摄像头不存在")
		}
		l.Errorf("find camera failed: %v", err)
		return nil, errorx.NewError(errorx.ErrVideoStream, "查询摄像头失败")
	}
	// 离线/故障设备不下发地址: 返回一个必然拉不通的 URL 只会把问题转移到播放器侧.
	if camera.Status != model.CameraStatusOnline {
		return nil, errorx.NewError(errorx.ErrVideoCameraOffline, "设备离线或故障, 无法取流")
	}

	expires := l.svcCtx.Config.Stream.ExpiresSeconds
	if expires <= 0 {
		expires = 3600
	}

	return &types.StreamResp{
		CameraId:  camera.ID,
		RtspUrl:   camera.RtspURL,
		FlvUrl:    flvURL(l.svcCtx.Config.Stream.FlvBaseURL, camera.DeviceID),
		ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second).Unix(),
	}, nil
}

// flvURL 拼接 FLV 拉流地址; 未配置流媒体服务时返回空串(不编造不可用的地址).
func flvURL(base, deviceID string) string {
	base = strings.TrimSpace(base)
	if base == "" || deviceID == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + "/" + deviceID + ".flv"
}
