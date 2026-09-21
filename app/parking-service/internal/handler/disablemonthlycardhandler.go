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

// DisableMonthlyCardHandler 月卡停用 HTTP 处理器(P2, 幂等).
func DisableMonthlyCardHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.MonthlyCardIdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "请求参数解析失败"))
			return
		}

		l := logic.NewDisableMonthlyCardLogic(r.Context(), svcCtx)
		resp, err := l.DisableMonthlyCard(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
