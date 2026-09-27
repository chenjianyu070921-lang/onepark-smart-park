package tdengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// BenchmarkBuildInsertSQL 基准遥测点写入 SQL 组装(含 deviceId 白名单化 + 字符串转义, 防注入),
// 贴近高频遥测落库热路径的局部开销, 作为对照基线.
func BenchmarkBuildInsertSQL(b *testing.B) {
	p := Point{
		DeviceID: `dev-001\x'`,
		Metric:   "tem'p",
		Value:    23.5,
		Quality:  0,
		Ts:       time.UnixMilli(1730000000000),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildInsertSQL(p)
	}
}

// BenchmarkEscapeTDString 基准 TDengine 字符串转义(防 SQL 注入的核心纯函数), 作为对照基线.
func BenchmarkEscapeTDString(b *testing.B) {
	s := `dev-001\x' OR 1=1--`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = escapeTDString(s)
	}
}

// benchREST 起进程内 httptest 服务器仿真 TDengine taosAdapter REST 端点(/rest/sql),
// 返回已启用的 wrapper 客户端; 无需外部 TDengine. 处理器对任意 SQL 返回成功响应.
func benchREST(b *testing.B) *Client {
	b.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"desc":"success","status":"succ"}`))
	}))
	b.Cleanup(srv.Close)
	// Endpoint 非空即启用; Database 默认 onepark_ts, 超时给足避免压测时触发.
	return NewClient(Conf{Endpoint: srv.URL, Database: "onepark_ts", Timeout: 5000})
}

// BenchmarkExec 基准 wrapper 真实 HTTP 写入往返: 组装请求 -> POST /rest/sql -> 解析 TDengine JSON 响应.
// 覆盖高频遥测落库的真实网络栈, 而非仅 SQL 字符串拼接.
func BenchmarkExec(b *testing.B) {
	cli := benchREST(b)
	ctx := context.Background()
	sql := "INSERT INTO t_dev VALUES (1730000000000, 'temperature', 23.5, 0)"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.Exec(ctx, sql); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWritePoint 基准 wrapper 真实遥测写入热路径: 拼 SQL(含 deviceId 白名单化 + 转义) -> HTTP -> JSON 解析.
func BenchmarkWritePoint(b *testing.B) {
	cli := benchREST(b)
	ctx := context.Background()
	p := Point{
		DeviceID: "dev-001",
		Metric:   "temperature",
		Value:    23.5,
		Quality:  0,
		Ts:       time.UnixMilli(1730000000000),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.WritePoint(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
}
