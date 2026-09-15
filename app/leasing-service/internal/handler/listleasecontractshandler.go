package handler

import (
	"net/http"

	"onepark/app/leasing-service/internal/logic"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func ListLeaseContractsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListLeaseReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewListLeaseContractsLogic(r.Context(), svcCtx)
		resp, err := l.ListLeaseContracts(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			response.Ok(w, resp)
		}
	}
}
