package mqtt

import "testing"

// BenchmarkCmdDownTopic 基准指令下行 topic 拼接(纯字符串格式化, 下行发布热路径的局部开销).
func BenchmarkCmdDownTopic(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CmdDownTopic("dev-001-ABCD")
	}
}
