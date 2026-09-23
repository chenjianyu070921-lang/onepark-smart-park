package jwt

import "testing"

// benchSecret 压测专用密钥(非生产值).
const benchSecret = "bench-secret-0123456789abcdef"

// BenchmarkGenerate 基准 JWT 签发开销(登录/刷新双令牌签发热路径).
func BenchmarkGenerate(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Generate(benchSecret, 42, "1,2,3", 7, TypeAccess, 3600); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParse 基准 JWT 校验开销(网关 Verify 每请求必走的热路径).
func BenchmarkParse(b *testing.B) {
	tok, err := Generate(benchSecret, 42, "1,2,3", 7, TypeAccess, 3600)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(benchSecret, tok); err != nil {
			b.Fatal(err)
		}
	}
}
