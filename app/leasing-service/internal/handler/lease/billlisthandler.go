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

func BillListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.BillListReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := lease.NewBillListLogic(r.Context(), svcCtx)
		resp, err := l.BillList(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
