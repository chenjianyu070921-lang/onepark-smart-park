package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/user-manage/internal/logic"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
)

func UsermanageHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewUsermanageLogic(r.Context(), svcCtx)
		resp, err := l.Usermanage(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
