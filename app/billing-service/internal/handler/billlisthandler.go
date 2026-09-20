package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/billing-service/internal/logic"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// BillListHandler 接口62: GET /api/billing/bills
func BillListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.BillListRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewBillListLogic(r.Context(), svcCtx)
		resp, err := l.BillList(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
