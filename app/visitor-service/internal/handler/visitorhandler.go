package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/visitor-service/internal/logic"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
)

func VisitorHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewVisitorLogic(r.Context(), svcCtx)
		resp, err := l.Visitor(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
