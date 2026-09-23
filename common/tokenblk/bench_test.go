package tokenblk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"onepark/common/redisx"
)

// benchRedis 起进程内 miniredis, 供 tokenblk 基准使用(无需外部 Redis).
func benchRedis(b *testing.B) *redisx.Client {
	b.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis 启动失败: %v", err)
	}
	b.Cleanup(mr.Close)
	return redisx.NewClient(&redisx.RedisConf{Addr: mr.Addr()})
}

// BenchmarkRevoke 基准 access 令牌吊销(黑名单写入)开销, 每操作一独立 jti 避免键复用失真.
func BenchmarkRevoke(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Revoke(ctx, rdb, fmt.Sprintf("jti-%d", i), time.Hour); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIsRevoked 基准黑名单查询开销(命中路径): 先写入固定 jti 再循环查询,
// 贴近"已注销令牌被 Verify 校验"的真实热路径.
func BenchmarkIsRevoked(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	if err := Revoke(ctx, rdb, "hot", time.Hour); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !IsRevoked(ctx, rdb, "hot") {
			b.Fatal("应为已吊销")
		}
	}
}

// BenchmarkStoreRefresh 基准 refresh 令牌登记(写入)开销.
func BenchmarkStoreRefresh(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := StoreRefresh(ctx, rdb, fmt.Sprintf("rjti-%d", i), 7*24*time.Hour); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRefreshExists 基准 refresh 登记表查询开销(命中路径).
func BenchmarkRefreshExists(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	if err := StoreRefresh(ctx, rdb, "hot", 7*24*time.Hour); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !RefreshExists(ctx, rdb, "hot") {
			b.Fatal("应已登记")
		}
	}
}

// BenchmarkRevokeRefresh 基准 refresh 吊销(删除)开销: 每轮先登记后吊销, 独立 jti.
func BenchmarkRevokeRefresh(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jti := fmt.Sprintf("rjti-%d", i)
		if err := StoreRefresh(ctx, rdb, jti, 7*24*time.Hour); err != nil {
			b.Fatal(err)
		}
		if err := RevokeRefresh(ctx, rdb, jti); err != nil {
			b.Fatal(err)
		}
	}
}
