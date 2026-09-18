package handler

import (
	"net/http"

	"onepark/app/video-service/internal/logic"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// AddCameraHandler 添加摄像头(docs/m3/04 #49): 统一响应体 {code,msg,data}.
func AddCameraHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AddCameraReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewAddCameraLogic(r.Context(), svcCtx)
		resp, err := l.AddCamera(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoCameraCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// ListCamerasHandler 摄像头列表(#50): 统一响应体 {code,msg,data}.
func ListCamerasHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListCamerasReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListCamerasLogic(r.Context(), svcCtx)
		resp, err := l.ListCameras(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoStream))
			return
		}
		response.Ok(w, resp)
	}
}

// GetStreamHandler 获取视频流地址(#51): 统一响应体 {code,msg,data}.
func GetStreamHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewGetStreamLogic(r.Context(), svcCtx)
		resp, err := l.GetStream(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoStream))
			return
		}
		response.Ok(w, resp)
	}
}

// GetCameraHandler 摄像头详情(地址簿维护): 统一响应体 {code,msg,data}.
func GetCameraHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewGetCameraLogic(r.Context(), svcCtx)
		resp, err := l.GetCamera(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoCameraNotFound))
			return
		}
		response.Ok(w, resp)
	}
}

// UpdateCameraHandler 修改摄像头(地址簿维护): 统一响应体 {code,msg,data}.
func UpdateCameraHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateCameraReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewUpdateCameraLogic(r.Context(), svcCtx)
		resp, err := l.UpdateCamera(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoCameraCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// DeleteCameraHandler 删除摄像头(地址簿下架): 统一响应体 {code,msg,data}.
func DeleteCameraHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewDeleteCameraLogic(r.Context(), svcCtx)
		resp, err := l.DeleteCamera(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoCameraCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// toVideoCodeError 已是业务错误则原样返回(保留细分码), 否则包成兜底错误码.
func toVideoCodeError(err error, fallback string) *errorx.CodeError {
	if ce, ok := err.(*errorx.CodeError); ok {
		return ce
	}
	return errorx.NewError(fallback, err.Error())
}
