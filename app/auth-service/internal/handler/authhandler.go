package handler

import (
	"net/http"

	"onepark/app/auth-service/internal/logic"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func AuthHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewAuthLogic(r.Context(), svcCtx)
		resp, err := l.Auth(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
