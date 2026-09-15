package handler

import (
	"net/http"

	"onepark/app/dashboard-service/internal/logic"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func QueryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := logic.NewQueryLogic(r.Context(), svcCtx)
		resp, err := l.Query()
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			response.Ok(w, resp)
		}
	}
}
