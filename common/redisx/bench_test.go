package redisx

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// benchRedis 起进程内 miniredis, 供 redisx 基准使用(无需外部 Redis).
func benchRedis(b *testing.B) *Client {
	b.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis 启动失败: %v", err)
	}
	b.Cleanup(mr.Close)
	return NewClient(&RedisConf{Addr: mr.Addr()})
}

// BenchmarkSet 基准 Redis 写入(Set)开销, 独立 key 避免复用失真.
func BenchmarkSet(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := rdb.Set(ctx, fmt.Sprintf("k-%d", i), "v", time.Minute).Err(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGet 基准 Redis 读取(Get)开销(命中路径): 先写入固定 key 再循环读取.
func BenchmarkGet(b *testing.B) {
	rdb := benchRedis(b)
	ctx := context.Background()
	if err := rdb.Set(ctx, "hot", "v", time.Minute).Err(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rdb.Get(ctx, "hot").Result(); err != nil {
			b.Fatal(err)
		}
	}
}
