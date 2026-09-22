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

// CreateRecordPlanHandler 创建录像计划: 统一响应体 {code,msg,data}.
func CreateRecordPlanHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateRecordPlanReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewCreateRecordPlanLogic(r.Context(), svcCtx)
		resp, err := l.CreateRecordPlan(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoRecordPlanCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// ListRecordPlansHandler 录像计划列表.
func ListRecordPlansHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListRecordPlansReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListRecordPlansLogic(r.Context(), svcCtx)
		resp, err := l.ListRecordPlans(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoRecordPlanCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// GetRecordPlanHandler 录像计划详情.
func GetRecordPlanHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewGetRecordPlanLogic(r.Context(), svcCtx)
		resp, err := l.GetRecordPlan(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoRecordPlanNotFound))
			return
		}
		response.Ok(w, resp)
	}
}

// UpdateRecordPlanHandler 修改录像计划.
func UpdateRecordPlanHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateRecordPlanReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewUpdateRecordPlanLogic(r.Context(), svcCtx)
		resp, err := l.UpdateRecordPlan(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoRecordPlanCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// DeleteRecordPlanHandler 删除录像计划.
func DeleteRecordPlanHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewDeleteRecordPlanLogic(r.Context(), svcCtx)
		resp, err := l.DeleteRecordPlan(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoRecordPlanCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// PlaybackHandler 回放查询: 按计划推导可用录像窗口并签发回放地址.
func PlaybackHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.PlaybackReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrVideoParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewPlaybackLogic(r.Context(), svcCtx)
		resp, err := l.Playback(&req)
		if err != nil {
			response.Fail(w, toVideoCodeError(err, errorx.ErrVideoPlayback))
			return
		}
		response.Ok(w, resp)
	}
}
