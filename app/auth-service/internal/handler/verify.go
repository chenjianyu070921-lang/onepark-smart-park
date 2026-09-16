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

// VerifyHandler 公开令牌校验: 任意调用方(含未登录)传入令牌即可探测有效性,
// 不要求预置 Authorization, 故需在网关 SkipPaths 与 auth-service publicPaths 同时放行.
func VerifyHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.VerifyReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		l := logic.NewVerifyLogic(r.Context(), svcCtx)
		resp, err := l.Verify(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
