package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/notice-service/internal/logic"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
)

func NoticeHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewNoticeLogic(r.Context(), svcCtx)
		resp, err := l.Notice(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
