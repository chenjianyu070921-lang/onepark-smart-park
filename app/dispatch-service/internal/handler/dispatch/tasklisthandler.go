// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package dispatch

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/app/dispatch-service/internal/logic/dispatch"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
)

func TaskListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.TaskListReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := dispatch.NewTaskListLogic(r.Context(), svcCtx)
		resp, err := l.TaskList(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
