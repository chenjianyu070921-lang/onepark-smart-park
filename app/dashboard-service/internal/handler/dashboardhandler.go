package handler

import (
	"net/http"

	"onepark/app/dashboard-service/internal/logic"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func DashboardHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewDashboardLogic(r.Context(), svcCtx)
		resp, err := l.Dashboard(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
