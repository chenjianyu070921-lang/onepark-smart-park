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

// LogoutHandler 注销令牌: 调用方携带待注销令牌(access/refresh), 其 jti 进黑名单.
// 与 Verify 同属公开端点(网关 SkipPaths 与 auth-service publicPaths 同时放行), 无需预置有效 Authorization.
func LogoutHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.LogoutReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		l := logic.NewLogoutLogic(r.Context(), svcCtx)
		if err := l.Logout(&req); err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, &types.LogoutResp{})
	}
}
