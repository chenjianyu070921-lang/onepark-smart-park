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

// AskHandler POST /api/kb/ask
func AskHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AskRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewAskLogic(r.Context(), svcCtx).Ask(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
