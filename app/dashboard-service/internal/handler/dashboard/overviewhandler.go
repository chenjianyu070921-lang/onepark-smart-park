// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package dashboard

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/dashboard-service/internal/logic/dashboard"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
)

func OverviewHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.OverviewReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := dashboard.NewOverviewLogic(r.Context(), svcCtx)
		resp, err := l.Overview(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
