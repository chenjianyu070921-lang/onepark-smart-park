// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package lease

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/leasing-service/internal/logic/lease"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
)

func BillSummaryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.BillSummaryReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := lease.NewBillSummaryLogic(r.Context(), svcCtx)
		resp, err := l.BillSummary(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
