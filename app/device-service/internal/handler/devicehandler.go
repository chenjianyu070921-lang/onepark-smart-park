package handler

import (
	"net/http"

	"onepark/app/device-service/internal/logic"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func DeviceHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.Request
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewDeviceLogic(r.Context(), svcCtx)
		resp, err := l.Device(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
