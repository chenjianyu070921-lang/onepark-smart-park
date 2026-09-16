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
//
// 鉴权策略: 全接口强制 JWT. 仅"鉴权引导端点"(login/refresh/verify)对公众开,
// 因为获取 Token 本身不能要求先持有 Token(否则无法登录); 其余所有接口
// (含 /api/users、/api/roles、/api/users/roles 等业务接口)无有效 Token 一律 401.
// 该公开集合必须与 auth-service 的 publicPaths 保持一致.
//
// 这是 RBAC 数据权限生效的前置阻塞项: 只有网关注入 x-tenant-id, 下游才能按租户隔离.
func Auth(secret string) func(http.HandlerFunc) http.HandlerFunc {
	// publicPaths 鉴权引导端点(获取/刷新 Token 的入口), 无需 JWT 即可访问.
	publicPaths := map[string]bool{
		"/api/auth/login":   true,
		"/api/auth/refresh": true,
		"/api/auth/verify":  true,
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
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
