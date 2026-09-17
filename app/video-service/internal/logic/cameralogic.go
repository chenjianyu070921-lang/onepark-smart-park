package logic

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// AddCameraLogic 添加摄像头(docs/m3/04 #49).
type AddCameraLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAddCameraLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AddCameraLogic {
	return &AddCameraLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *AddCameraLogic) AddCamera(req *types.AddCameraReq) (*types.AddCameraResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Cameras == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "摄像头存储未就绪(MySQL 未配置)")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "name 不能为空")
	}
	deviceID := strings.TrimSpace(req.DeviceId)
	if deviceID == "" {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "device_id 不能为空")
	}
	rtspURL := strings.TrimSpace(req.RtspUrl)
	// RTSP 地址在创建期校验: 地址写错只能等到取流时才暴露, 排查成本高(docs/m3/04 #49 的 M3-E-3002).
	if !isRtspURL(rtspURL) {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "rtsp_url 需以 rtsp:// 或 rtsps:// 开头")
	}
	if req.Status != 0 && req.Status != model.CameraStatusOnline && req.Status != model.CameraStatusFault {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "status 仅支持 0(离线)/1(在线)/2(故障)")
	}

	location, err := marshalLocation(req.Location)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, err.Error())
	}

	now := time.Now()
	c := &model.Camera{
		Name:      name,
		DeviceID:  deviceID,
		AreaID:    req.AreaId,
		RtspURL:   rtspURL,
		Location:  location,
		Status:    req.Status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	c.TenantID = tenantID

	if err := l.svcCtx.Cameras.Create(l.ctx, c); err != nil {
		if err == model.ErrCameraDuplicate {
			return nil, errorx.NewError(errorx.ErrVideoCameraCreate, "该设备ID已登记过摄像头")
		}
		l.Errorf("create camera failed: %v", err)
		return nil, errorx.NewError(errorx.ErrVideoCameraCreate, "添加摄像头失败")
	}
	return &types.AddCameraResp{Id: c.ID}, nil
}

// isRtspURL 校验 RTSP(S) 拉流地址格式: 协议必须是 rtsp/rtsps 且必须带主机,
// 仅做前缀判断会把 "rtsp://" 这种没有主机的地址放过去.
func isRtspURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "rtsp" || scheme == "rtsps"
}

// marshalLocation 序列化位置信息; nil 返回 nil(写入 NULL).
func marshalLocation(l *types.Location) (*string, error) {
	if l == nil {
		return nil, nil
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "location 序列化失败")
	}
	s := string(raw)
	return &s, nil
}

// ListCamerasLogic 摄像头列表 + 在线状态(docs/m3/04 #50).
type ListCamerasLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListCamerasLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListCamerasLogic {
	return &ListCamerasLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *ListCamerasLogic) ListCameras(req *types.ListCamerasReq) (*types.ListCamerasResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Cameras == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "摄像头存储未就绪(MySQL 未配置)")
	}

	f := model.CameraListFilter{TenantID: tenantID, AreaID: req.AreaId, Page: int(req.Page), PageSize: int(req.PageSize)}
	// status 零值(离线)与"不筛选"同义不可区分, 约定: 负数表示不筛选.
	if req.Status >= 0 {
		if req.Status != model.CameraStatusOffline && req.Status != model.CameraStatusOnline && req.Status != model.CameraStatusFault {
			return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "status 仅支持 0(离线)/1(在线)/2(故障)")
		}
		status := req.Status
		f.Status = &status
	}

	list, total, err := l.svcCtx.Cameras.List(l.ctx, f)
	if err != nil {
		l.Errorf("list cameras failed: %v", err)
		return nil, errorx.NewError(errorx.ErrVideoStream, "查询摄像头列表失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	items := make([]types.CameraItem, 0, len(list))
	for _, c := range list {
		items = append(items, types.CameraItem{
			Id:              c.ID,
			Name:            c.Name,
			DeviceId:        c.DeviceID,
			AreaId:          c.AreaID,
			Status:          c.Status,
			LastHeartbeatAt: timePtrUnix(c.LastHeartbeatAt),
			CreatedAt:       c.CreatedAt.Unix(),
		})
	}
	return &types.ListCamerasResp{Total: total, Page: page, PageSize: size, List: items}, nil
}

// timePtrUnix 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timePtrUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
