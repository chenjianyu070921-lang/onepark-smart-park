package logic

import "testing"

// TestGenSecret 设备密钥应随机且长度足够(base64(32字节)=44 字符 + 时间戳后缀).
func TestGenSecret(t *testing.T) {
	s1 := genSecret(32)
	s2 := genSecret(32)

	if s1 == s2 {
		t.Fatal("两次生成的密钥相同, 随机源异常")
	}
	if len(s1) < 44 {
		t.Fatalf("密钥长度不足: got %d, 期望 >= 44", len(s1))
	}
}

// TestNormalizePage 分页参数兜底: 非法值回落默认值, 单页上限 100.
func TestNormalizePage(t *testing.T) {
	tests := []struct {
		name       string
		page, size int
		wantPage   int
		wantSize   int
	}{
		{"默认", 0, 0, 1, 20},
		{"负数", -1, -5, 1, 20},
		{"超上限", 3, 200, 3, 100},
		{"正常", 2, 50, 2, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, size := normalizePage(tt.page, tt.size)
			if page != tt.wantPage || size != tt.wantSize {
				t.Fatalf("normalizePage(%d,%d) = (%d,%d), 期望 (%d,%d)",
					tt.page, tt.size, page, size, tt.wantPage, tt.wantSize)
			}
		})
	}
}
