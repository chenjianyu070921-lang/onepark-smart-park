package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/energy-agent-service/internal/logic"
	"onepark/app/energy-agent-service/internal/svc"
	"onepark/app/energy-agent-service/internal/types"
)

// SuggestionListHandler 建议列表: GET /api/agent/suggestions
func SuggestionListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SuggestionListRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewSuggestionListLogic(r.Context(), svcCtx)
		resp, err := l.SuggestionList(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
