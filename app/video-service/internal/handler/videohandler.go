package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/video-service/internal/logic"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
)

func VideoHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewVideoLogic(r.Context(), svcCtx)
		resp, err := l.Video(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
