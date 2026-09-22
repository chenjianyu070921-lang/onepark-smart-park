package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	authpb "onepark/proto/auth"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/response"
)

// Auth 网关统一鉴权中间件: 提取 Bearer Token, 委托 auth-service gRPC Verify 校验并取回身份,
// 再向转发请求注入身份 Header(x-user-id / x-role-ids / x-tenant-id), 供下游业务服务通过
// common/middleware.IdentityFromHeader 提升进 ctxdata.
//
// 鉴权策略: 全接口强制校验. 仅"鉴权引导端点"(login/refresh/verify/logout)对公众开,
// 因为获取 Token 本身不能要求先持有 Token(否则无法登录); 其余所有接口无有效 Token 一律 401.
// 该公开集合必须与 auth-service 视为公开的路径保持一致.
//
// 这是 RBAC 数据权限生效的前置阻塞项: 只有网关注入 x-tenant-id, 下游才能按租户隔离.
// Token 吊销(注销)语义已收口到 auth-service.Verify(查 Redis 黑名单), 网关不再本地查黑名单,
// 避免两处不一致导致"注销不失效". 网关调用 Verify 失败(含 auth-service 不可达)统一按 401 失败闭环.
func Auth(client authpb.AuthServiceClient) func(http.HandlerFunc) http.HandlerFunc {
	// publicPaths 鉴权引导端点(获取/刷新/校验/注销 Token 的入口), 无需校验即可访问.
	// 注: 网关 /health 是显式注册路由, 不走下方 NotFoundHandler 包裹的鉴权链, 故天然公开, 无需在此登记.
	publicPaths := map[string]bool{
		"/api/auth/login":   true,
		"/api/auth/refresh": true,
		"/api/auth/verify":  true,
		"/api/auth/logout":  true,
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
				next(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == "" && strings.HasPrefix(r.URL.Path, "/ws/") {
				// WebSocket 握手浏览器无法携带 Authorization 头, 仅对 /ws/ 路径允许从 ?token= 读取.
				token = r.URL.Query().Get("token")
			}
			if token == "" {
				response.Fail(w, errorx.NewError(errorx.ErrUnauthorized, "缺少身份凭证"))
				return
			}
			// 委托 auth-service gRPC 校验并取回身份(签名/过期/吊销一站式, 见 auth-service Verify).
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			resp, err := client.Verify(ctx, &authpb.VerifyReq{Token: token})
			if err != nil || resp == nil || !resp.Valid {
				response.Fail(w, errorx.NewError(errorx.ErrUnauthorized, "身份凭证无效或已过期"))
				return
			}
			// 注入身份 Header, 下游服务通过 IdentityFromHeader 提升进 ctxdata.
			r.Header.Set(ctxdata.CtxUserId, strconv.FormatInt(resp.UserId, 10))
			r.Header.Set(ctxdata.CtxRoleIds, resp.RoleIds)
			// tenant 未回填(tenant_id==0)时不注入 x-tenant-id, 交由 proxy 的 DefaultTenantId 兜底,
			// 避免向下游注入 0 触发"缺少租户信息"400(登录目前未回填租户维度, 见 auth-service login.go).
			if resp.TenantId != 0 {
				r.Header.Set(ctxdata.CtxTenantId, strconv.FormatInt(resp.TenantId, 10))
			}
			// 透传 request-id, 保证链路追踪连续性.
			if rid := ctxdata.GetRequestId(r.Context()); rid != "" {
				r.Header.Set(ctxdata.CtxRequestId, rid)
			}
			next(w, r)
		}
	}
}
