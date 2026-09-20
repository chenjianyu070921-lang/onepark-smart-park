package middleware

import (
	"net/http"
	"strconv"

	"onepark/common/ctxdata"
)

// IdentityFromHeader 将网关注入的身份 Header(x-user-id / x-role-ids / x-tenant-id / x-data-scope)
// 提升进 ctxdata, 供业务服务 logic 层通过 ctxdata.GetXxx 读取.
// 与网关 internal/middleware.Auth 配套: 网关验 JWT 并注入 Header, 业务服务用本中间件一行接入.
// 适用于 go-zero rest 服务: server.Use(middleware.IdentityFromHeader).
func IdentityFromHeader(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if v := r.Header.Get(ctxdata.CtxUserId); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx = ctxdata.SetUserId(ctx, id)
			}
		}
		if v := r.Header.Get(ctxdata.CtxRoleIds); v != "" {
			ctx = ctxdata.SetRoleIds(ctx, v)
		}
		if v := r.Header.Get(ctxdata.CtxTenantId); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx = ctxdata.SetTenantId(ctx, id)
			}
		}
		if v := r.Header.Get(ctxdata.CtxDataScope); v != "" {
			if s, err := strconv.ParseInt(v, 10, 8); err == nil {
				ctx = ctxdata.SetDataScope(ctx, int8(s))
			}
		}
		next(w, r.WithContext(ctx))
	}
}
