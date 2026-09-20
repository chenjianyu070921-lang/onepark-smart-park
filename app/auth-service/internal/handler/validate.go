package handler

import (
	"net/http"

	"onepark/app/auth-service/internal/logic"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func ValidateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ValidateReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		l := logic.NewValidateLogic(r.Context(), svcCtx)
		resp, err := l.Validate(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
