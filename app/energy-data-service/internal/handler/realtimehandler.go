package handler

import (
	"errors"
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/energy-data-service/internal/logic"
	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"
)

// RealtimeHandler 接口52: GET /api/energy/realtime/:deviceId
func RealtimeHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.RealtimeRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewRealtimeLogic(r.Context(), svcCtx)
		resp, err := l.Realtime(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}

// writeErr 统一把 error 转成团队的 {code, msg} 响应
func writeErr(w http.ResponseWriter, err error) {
	var ce *errorx.CodeError
	if errors.As(err, &ce) {
		response.Fail(w, ce)
		return
	}
	response.FailWith(w, errorx.ErrInternal, err.Error())
}
