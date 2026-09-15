package handler

import (
	"net/http"

	"onepark/app/parking-service/internal/logic"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ParkingExitHandler 车辆离场计费: 统一响应体 {code,msg,data}.
func ParkingExitHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ParkingExitReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewParkingExitLogic(r.Context(), svcCtx)
		resp, err := l.ParkingExit(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
