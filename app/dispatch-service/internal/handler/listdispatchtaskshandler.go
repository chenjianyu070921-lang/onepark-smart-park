package handler

import (
	"net/http"

	"onepark/app/dispatch-service/internal/logic"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func ListDispatchTasksHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListDispatchReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewListDispatchTasksLogic(r.Context(), svcCtx)
		resp, err := l.ListDispatchTasks(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			response.Ok(w, resp)
		}
	}
}
