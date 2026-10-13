package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"onepark/app/kb-service/internal/logic/kb"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"
)

// ListQaLogsHandler GET /api/kb/qa-logs
func ListQaLogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListQaLogsRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewFeedbackLogic(r.Context(), svcCtx).Logs(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

// FeedbackHandler POST /api/kb/feedback
func FeedbackHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.FeedbackRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewFeedbackLogic(r.Context(), svcCtx).Feedback(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
