package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/access-control-service/internal/logic"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
)

func AccesscontrolHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewAccesscontrolLogic(r.Context(), svcCtx)
		resp, err := l.Accesscontrol(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
