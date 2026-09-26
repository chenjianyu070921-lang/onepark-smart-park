package tdengine

import (
	"testing"
	"time"
)

// BenchmarkBuildInsertSQL 基准遥测点写入 SQL 组装(含 deviceId 白名单化 + 字符串转义, 防注入),
// 贴近高频遥测落库热路径.
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

// BenchmarkEscapeTDString 基准 TDengine 字符串转义(防 SQL 注入的核心纯函数).
func BenchmarkEscapeTDString(b *testing.B) {
	s := `dev-001\x' OR 1=1--`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = escapeTDString(s)
	}
}
