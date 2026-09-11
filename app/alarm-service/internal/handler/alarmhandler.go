package handler

import (
	"net/http"

	"onepark/app/alarm-service/internal/logic"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func AlarmHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewAlarmLogic(r.Context(), svcCtx)
		resp, err := l.Alarm(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
