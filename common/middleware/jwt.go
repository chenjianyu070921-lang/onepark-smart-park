// Package middleware 中的 JWT 鉴权中间件.
// 校验网关/认证服务签发的 HS256 Token, 解析出的用户/租户/角色写入 ctxdata 供 logic 使用.
// Secret 为空时自动放行(开发联调期), 并打印告警, 避免未完成 M6 认证服务前全站 401.
package middleware

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v4"

	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// unauthorized 统一 401 响应; 未授权不属于业务错误码的 500 级别, 此处直接指定状态码.
func unauthorized(w http.ResponseWriter, msg string) {
	httpx.WriteJson(w, http.StatusUnauthorized, &response.Body{
		Code: errorx.ErrUnauthorized,
		Msg:  msg,
	})
}

// 白名单路径: 健康检查与静态资源不做鉴权.
var noAuthPaths = map[string]struct{}{
	"/health":  {},
	"/ping":    {},
	"/favicon": {},
}

// JWT 返回 JWT 鉴权中间件; secret 为空时降级为透传.
func JWT(secret string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := noAuthPaths[r.URL.Path]; ok {
				next(w, r)
				return
			}

			if secret == "" {
				logx.Errorf("[JWT] 未配置 JWT secret, 请求放行: path=%s", r.URL.Path)
				next(w, r)
				return
			}

			tokenStr := extractToken(r)
			if tokenStr == "" {
				unauthorized(w, "缺少 Authorization Bearer Token")
				return
			}

			claims := jwt.MapClaims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, func(*jwt.Token) (any, error) {
				return []byte(secret), nil
			})
			if err != nil || !token.Valid {
				if errors.Is(err, jwt.ErrTokenExpired) {
					unauthorized(w, "Token 已过期")
					return
				}
				unauthorized(w, "Token 非法")
				return
			}

			ctx := r.Context()
			if v, ok := claims[ctxdata.CtxUserId].(string); ok {
				if uid, err := strconv.ParseInt(v, 10, 64); err == nil {
					ctx = ctxdata.SetUserId(ctx, uid)
				}
			} else if v, ok := claims[ctxdata.CtxUserId].(float64); ok {
				ctx = ctxdata.SetUserId(ctx, int64(v))
			}
			if v, ok := claims[ctxdata.CtxTenantId].(string); ok {
				if tid, err := strconv.ParseInt(v, 10, 64); err == nil {
					ctx = ctxdata.SetTenantId(ctx, tid)
				}
			} else if v, ok := claims[ctxdata.CtxTenantId].(float64); ok {
				ctx = ctxdata.SetTenantId(ctx, int64(v))
			}
			if v, ok := claims[ctxdata.CtxRoleIds].(string); ok {
				ctx = ctxdata.SetRoleIds(ctx, v)
			}

			next(w, r.WithContext(ctx))
		}
	}
}

func extractToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return r.URL.Query().Get("token")
	}
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return strings.TrimSpace(header)
}
