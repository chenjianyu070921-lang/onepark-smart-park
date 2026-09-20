package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/billing-service/internal/logic"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// RuleCreateHandler 接口59: POST /api/billing/rule
func RuleCreateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.RuleCreateRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewRuleCreateLogic(r.Context(), svcCtx)
		resp, err := l.RuleCreate(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
