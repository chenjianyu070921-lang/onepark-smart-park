package handler

import (
	"net/http"

	"onepark/app/energy-data-service/internal/logic"
	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func EnergydataHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewEnergydataLogic(r.Context(), svcCtx)
		resp, err := l.Energydata(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
