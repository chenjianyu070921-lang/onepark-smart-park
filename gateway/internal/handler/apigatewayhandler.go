package handler

import (
	"net/http"

	"onepark/gateway/internal/logic"
	"onepark/gateway/internal/svc"
	"onepark/gateway/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func ApigatewayHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewApigatewayLogic(r.Context(), svcCtx)
		resp, err := l.Apigateway(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
