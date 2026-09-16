package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/energy-analysis-service/internal/logic"
	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"
)

// DailyHandler 接口56: GET /api/energy/daily?date=2026-09-15
func DailyHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.DailyRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewDailyLogic(r.Context(), svcCtx)
		resp, err := l.Daily(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
