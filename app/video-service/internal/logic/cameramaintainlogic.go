package logic

import (
	"context"
	"strings"

	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// GetCameraLogic 摄像头详情(地址簿维护面).
// 与 #51 GetStream 的区别: 详情返回地址簿元信息(含 rtsp_url 原文),
// GetStream 只返回"可下发的流地址 + 有效期", 且拒绝离线/故障设备.
type GetCameraLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetCameraLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetCameraLogic {
	return &GetCameraLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *GetCameraLogic) GetCamera(req *types.IdReq) (*types.CameraDetailResp, error) {
	tenantID, err := cameraGuard(l.ctx, l.svcCtx, req.Id)
	if err != nil {
		return nil, err
	}

	c, err := l.svcCtx.Cameras.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrCameraNotFound {
			return nil, errorx.NewError(errorx.ErrVideoCameraNotFound, "摄像头不存在")
		}
		l.Errorf("find camera failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoStream, "查询摄像头失败")
	}
	return &types.CameraDetailResp{
		Id:              c.ID,
		Name:            c.Name,
		DeviceId:        c.DeviceID,
		AreaId:          c.AreaID,
		RtspUrl:         c.RtspURL,
		Status:          c.Status,
		LastHeartbeatAt: timePtrUnix(c.LastHeartbeatAt),
		CreatedAt:       c.CreatedAt.Unix(),
		UpdatedAt:       c.UpdatedAt.Unix(),
	}, nil
}

// UpdateCameraLogic 修改摄像头(地址簿维护).
//
// 为什么必须存在: rtsp_url 在创建期做了强校验, 但设备换 IP / 换流地址是常态;
// 没有修改接口时, 地址写错的摄像头既改不了(device_id 唯一, 重新登记会被拒)
// 又删不掉, 只能直连数据库改行 —— 地址簿因此基本不可维护.
type UpdateCameraLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateCameraLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateCameraLogic {
	return &UpdateCameraLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UpdateCameraLogic) UpdateCamera(req *types.UpdateCameraReq) (*types.UpdateCameraResp, error) {
	tenantID, err := cameraGuard(l.ctx, l.svcCtx, req.Id)
	if err != nil {
		return nil, err
	}

	patch := model.CameraPatch{}
	if name := strings.TrimSpace(req.Name); name != "" {
		patch.Name = &name
	}
	if req.AreaId != nil {
		areaID := *req.AreaId
		patch.AreaID = &areaID
	}
	// rtsp_url 在修改时同样要校验: 创建期校验挡不住"改成一个坏地址".
	if rtsp := strings.TrimSpace(req.RtspUrl); rtsp != "" {
		if !isRtspURL(rtsp) {
			return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "rtsp_url 需以 rtsp:// 或 rtsps:// 开头")
		}
		patch.RtspURL = &rtsp
	}
	if req.Location != nil {
		loc, err := marshalLocation(req.Location)
		if err != nil {
			return nil, errorx.NewError(errorx.ErrVideoParamInvalid, err.Error())
		}
		patch.Location = loc
	}
	if req.Status != nil {
		status := *req.Status
		if status != model.CameraStatusOffline && status != model.CameraStatusOnline && status != model.CameraStatusFault {
			return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "status 仅支持 0(离线)/1(在线)/2(故障)")
		}
		patch.Status = &status
	}

	if err := l.svcCtx.Cameras.Update(l.ctx, tenantID, req.Id, patch); err != nil {
		if err == model.ErrCameraNotFound {
			// 既可能是"ID 不存在", 也可能是"一个字段都没传"(存储层对空 patch 返回同一个错误)。
			// 合并成一个错误码但文案点明两种可能, 否则调用方会误判成摄像头丢了.
			return nil, errorx.NewError(errorx.ErrVideoCameraNotFound, "摄像头不存在或未传入任何待修改字段")
		}
		l.Errorf("update camera failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoCameraCreate, "修改摄像头失败")
	}
	return &types.UpdateCameraResp{Id: req.Id}, nil
}

// DeleteCameraLogic 删除摄像头(地址簿下架).
type DeleteCameraLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeleteCameraLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteCameraLogic {
	return &DeleteCameraLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *DeleteCameraLogic) DeleteCamera(req *types.IdReq) (*types.DeleteCameraResp, error) {
	tenantID, err := cameraGuard(l.ctx, l.svcCtx, req.Id)
	if err != nil {
		return nil, err
	}
	if err := l.svcCtx.Cameras.Delete(l.ctx, tenantID, req.Id); err != nil {
		if err == model.ErrCameraNotFound {
			return nil, errorx.NewError(errorx.ErrVideoCameraNotFound, "摄像头不存在")
		}
		l.Errorf("delete camera failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoCameraCreate, "删除摄像头失败")
	}
	return &types.DeleteCameraResp{Id: req.Id}, nil
}

// cameraGuard 统一校验租户 / 主键 / 存储可用性, 三个维护接口共用,
// 避免"某个接口忘了判 tenant_id"导致跨租户读写摄像头.
func cameraGuard(ctx context.Context, svcCtx *svc.ServiceContext, id int64) (int64, error) {
	tenantID := ctxdata.GetTenantId(ctx)
	if tenantID == 0 {
		return 0, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if id <= 0 {
		return 0, errorx.NewError(errorx.ErrVideoParamInvalid, "id 非法")
	}
	if svcCtx.Cameras == nil {
		return 0, errorx.NewError(errorx.ErrDepConnect, "摄像头存储未就绪(MySQL 未配置)")
	}
	return tenantID, nil
}
