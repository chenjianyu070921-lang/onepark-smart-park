package handler

import (
	"net/http"

	"onepark/app/energy-analysis-service/internal/logic"
	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func EnergyanalysisHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewEnergyanalysisLogic(r.Context(), svcCtx)
		resp, err := l.Energyanalysis(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
