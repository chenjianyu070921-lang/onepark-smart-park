package ctxdata

import (
	"context"
	"testing"
)

// BenchmarkSetGetIdentity 基准网关/中间件注入并读取身份上下文的开销
// (x-user-id / x-role-ids / x-tenant-id / x-request-id 全链路透传热路径).
func BenchmarkSetGetIdentity(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx := SetUserId(context.Background(), 42)
		ctx = SetRoleIds(ctx, "1,2,3")
		ctx = SetTenantId(ctx, 7)
		ctx = SetRequestId(ctx, "req-abc")
		_ = GetUserId(ctx)
		_ = GetRoleIds(ctx)
		_ = GetTenantId(ctx)
		_ = GetRequestId(ctx)
	}
}
