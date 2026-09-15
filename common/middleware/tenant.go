package middleware

import (
	"net/http"
	"strconv"

	"onepark/common/ctxdata"
)

// Tenant 从网关注入的 Header 读取 RBAC 上下文, 写入 context, 供 logic 层数据隔离使用.
// 读取 Header: x-tenant-id(园区ID) / x-user-id(用户ID) / x-role-ids(角色列表, 逗号分隔).
// 缺失或非法时降级为 0/空串(不过滤), 不阻断请求.
func Tenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if v, err := strconv.ParseInt(r.Header.Get(ctxdata.CtxTenantId), 10, 64); err == nil {
			ctx = ctxdata.SetTenantId(ctx, v)
		}
		if v, err := strconv.ParseInt(r.Header.Get(ctxdata.CtxUserId), 10, 64); err == nil {
			ctx = ctxdata.SetUserId(ctx, v)
		}
		if roles := r.Header.Get(ctxdata.CtxRoleIds); roles != "" {
			ctx = ctxdata.SetRoleIds(ctx, roles)
		}

		next(w, r.WithContext(ctx))
	}
}
