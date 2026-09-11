package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"onepark/common/ctxdata"
)

// RequestId 生成或透传 RequestId.
// 网关层生成, 服务层透传上游 Header.
func RequestIdMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(ctxdata.CtxRequestId)
		if rid == "" {
			rid = newRequestId()
		}
		w.Header().Set(ctxdata.CtxRequestId, rid)
		next(w, r.WithContext(ctxdata.SetRequestId(r.Context(), rid)))
	}
}

// newRequestId 简化版, 生产环境建议替换为 UUID 或 snowflake.
func newRequestId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "rid-" + hex.EncodeToString(b)
}
