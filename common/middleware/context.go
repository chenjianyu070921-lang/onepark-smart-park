package middleware

import (
	"net/http"
	"strconv"

	"onepark/common/ctxdata"
)

// ContextMiddleware 将网关注入的身份 Header 写入 context, 供 logic 层通过 ctxdata 读取.
// 安全前提: 仅可在网关后置的信任边界内启用, 网关必须无条件覆盖 x-tenant-id / x-user-id / x-role-ids,
// 否则客户端可伪造身份. 直连服务(未过网关)的本地联调依赖本中间件.
func ContextMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if v := r.Header.Get(ctxdata.CtxTenantId); v != "" {
			if tenantID, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx = ctxdata.SetTenantId(ctx, tenantID)
			}
		}
		if v := r.Header.Get(ctxdata.CtxUserId); v != "" {
			if userID, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx = ctxdata.SetUserId(ctx, userID)
			}
		}
		if v := r.Header.Get(ctxdata.CtxRoleIds); v != "" {
			ctx = ctxdata.SetRoleIds(ctx, v)
		}
		next(w, r.WithContext(ctx))
	}
}
