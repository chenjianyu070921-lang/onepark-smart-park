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
		// 交给 response.Init() 注册的全局 OkHandler 统一包装,
		// 此处若先 response.Ok 再入 httpx 会被包两层 {data:{code,msg,data}}.
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
