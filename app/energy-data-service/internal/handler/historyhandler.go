package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/energy-data-service/internal/logic"
	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"
)

// HistoryHandler 接口53: GET /api/energy/history
func HistoryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.HistoryRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewHistoryLogic(r.Context(), svcCtx)
		resp, err := l.History(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
