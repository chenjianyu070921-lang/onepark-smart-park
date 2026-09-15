package handler

import (
	"net/http"

	"onepark/app/workorder-service/internal/logic"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListWorkOrdersHandler 工单列表: 统一响应体 {code,msg,data}.
func ListWorkOrdersHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListWorkOrderReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewListWorkOrdersLogic(r.Context(), svcCtx)
		resp, err := l.ListWorkOrders(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
