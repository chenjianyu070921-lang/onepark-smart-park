package handler

import (
	"net/http"

	"onepark/app/device-service/internal/logic"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func DeviceCommandHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.DeviceCommandReq
		if err := httpx.Parse(r, &req); err != nil {
			response.FailWith(w, errorx.ErrBadRequest, err.Error())
			return
		}

		l := logic.NewDeviceCommandLogic(r.Context(), svcCtx)
		resp, err := l.DeviceCommand(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrInternal, err.Error())
			}
		} else {
			response.Ok(w, resp)
		}
	}
}
