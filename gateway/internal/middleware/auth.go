package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/jwt"
	"onepark/common/response"
)

// Auth 网关统一鉴权中间件: 校验 Bearer Token, 并向转发请求注入身份 Header,
// 供下游业务服务通过 common/middleware.IdentityFromHeader 提升进 ctxdata.
// skip 为公开路径白名单(精确匹配, 如 /api/auth/login); 命中直接放行.
//
// 这是 RBAC 数据权限生效的前置阻塞项: 只有网关注入 x-tenant-id, 下游才能按租户隔离.
func Auth(secret string, skip map[string]bool) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if skip[r.URL.Path] {
				next(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			if auth == "" {
				response.Fail(w, errorx.NewError(errorx.ErrUnauthorized, "缺少身份凭证"))
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")
			claims, err := jwt.Parse(secret, token)
			if err != nil || claims.Type != jwt.TypeAccess {
				response.Fail(w, errorx.NewError(errorx.ErrUnauthorized, "身份凭证无效或已过期"))
				return
			}
			// 注入身份 Header, 下游服务通过 IdentityFromHeader 提升进 ctxdata.
			r.Header.Set(ctxdata.CtxUserId, strconv.FormatInt(claims.UserId, 10))
			r.Header.Set(ctxdata.CtxRoleIds, claims.RoleIds)
			r.Header.Set(ctxdata.CtxTenantId, strconv.FormatInt(claims.TenantId, 10))
			// 透传 request-id, 保证链路追踪连续性.
			if rid := ctxdata.GetRequestId(r.Context()); rid != "" {
				r.Header.Set(ctxdata.CtxRequestId, rid)
			}
			next(w, r)
		}
	}
}
