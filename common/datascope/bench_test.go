package datascope

import (
	"context"
	"testing"

	"onepark/common/ctxdata"
)

// BenchmarkWhereOf_Tenant 基准"本园区/租户隔离" WHERE 片段计算(纯函数, 不依赖 gorm 连接).
func BenchmarkWhereOf_Tenant(b *testing.B) {
	ctx := ctxdata.SetTenantId(context.Background(), 42)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		whereOf(ctx)
	}
}

// BenchmarkWhereOf_All 基准"全部数据"分支(直接返回空过滤, 最短路径).
func BenchmarkWhereOf_All(b *testing.B) {
	ctx := ctxdata.SetDataScope(context.Background(), ScopeAll)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		whereOf(ctx)
	}
}

// BenchmarkWhereOf_Self 基准"本人"分支(create_by = ?).
func BenchmarkWhereOf_Self(b *testing.B) {
	ctx := ctxdata.SetDataScope(context.Background(), ScopeSelf)
	ctx = ctxdata.SetUserId(ctx, 7)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		whereOf(ctx)
	}
}
