package middleware

import (
	"net/http"
	"strconv"

	"onepark/common/ctxdata"
)

// Tenant 从网关注入的 x-tenant-id Header 读取园区/租户 ID, 写入 context,
// 供 logic 层做 RBAC 数据隔离与缓存 key 维度. 缺省为 0(不过滤).
func Tenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get(ctxdata.CtxTenantId)
		var tenantID int64
		if tid != "" {
			if v, err := strconv.ParseInt(tid, 10, 64); err == nil {
				tenantID = v
			}
		}
		next(w, r.WithContext(ctxdata.SetTenantId(r.Context(), tenantID)))
	}
}
