package middleware

import (
	"net/http"
	"strings"

	"onepark/common/jwt"
	"onepark/app/auth-service/internal/svc"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// publicPaths 公开路由白名单, 不经过 JWT 校验.
var publicPaths = map[string]bool{
	"/api/auth/login":   true,
	"/api/auth/refresh": true,
}

// JwtAuth 返回 go-zero 全局中间件: 校验 Authorization Bearer Token,
// 解析后注入 ctxdata; 失败(缺失/伪造/过期)返回 401. 白名单路由直接放行.
func JwtAuth(svcCtx *svc.ServiceContext) rest.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
				next(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				writeUnauthorized(w)
				return
			}
			claims, err := jwt.Parse(svcCtx.JwtSecret, strings.TrimPrefix(auth, "Bearer "))
			if err != nil || claims.Type != jwt.TypeAccess {
				writeUnauthorized(w)
				return
			}
			ctx := ctxdata.SetUserId(r.Context(), claims.UserId)
			ctx = ctxdata.SetRoleIds(ctx, claims.RoleIds)
			ctx = ctxdata.SetTenantId(ctx, claims.TenantId)
			next(w, r.WithContext(ctx))
		}
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	httpx.WriteJson(w, http.StatusUnauthorized, &response.Body{
		Code: errorx.ErrUnauthorized,
		Msg:  "token missing or invalid",
	})
}
