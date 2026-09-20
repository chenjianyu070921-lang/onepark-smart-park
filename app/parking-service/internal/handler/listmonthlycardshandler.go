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

// ListMonthlyCardsHandler 月卡分页列表 HTTP 处理器(P2, expiring_days 到期提醒筛选).
func ListMonthlyCardsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListMonthlyCardsReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "请求参数解析失败"))
			return
		}

		l := logic.NewListMonthlyCardsLogic(r.Context(), svcCtx)
		resp, err := l.ListMonthlyCards(&req)
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
