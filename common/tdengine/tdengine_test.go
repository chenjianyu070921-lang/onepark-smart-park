package tdengine

import (
	"strings"
	"testing"
	"time"
)

// TestEscapeTDString 验证反斜杠与单引号的转义顺序/结果.
func TestEscapeTDString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a'b", `a\'b`},
		{`a\b`, `a\\b`},
		// TDengine 注入 payload: 若只去单引号而不转义反斜杠, `\'` 会闭合字面量.
		{`x\' OR 1=1--`, `x\\\' OR 1=1--`},
	}
	for _, c := range cases {
		if got := escapeTDString(c.in); got != c.want {
			t.Errorf("escapeTDString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildInsertSQL_EscapesTagAndMetric 验证 deviceId/metric 被转义后再拼进 SQL.
func TestBuildInsertSQL_EscapesTagAndMetric(t *testing.T) {
	sql := buildInsertSQL(Point{
		DeviceID: "d'1",
		Metric:   "te'mp",
		Ts:       time.UnixMilli(1700000000000),
	})
	if !strings.Contains(sql, `TAGS('d\'1')`) {
		t.Fatalf("deviceId 未正确转义: %s", sql)
	}
	if !strings.Contains(sql, `'te\'mp'`) {
		t.Fatalf("metric 未正确转义: %s", sql)
	}
}

// TestBuildInsertSQL_InjectionPayloadNeutralized 验证反斜杠绕过型 payload 无法越界.
func TestBuildInsertSQL_InjectionPayloadNeutralized(t *testing.T) {
	sql := buildInsertSQL(Point{
		DeviceID: `x\' OR 1=1--`,
		Metric:   "m",
		Ts:       time.UnixMilli(1),
	})
	// 转义后, 危险 payload 只能作为字面量出现; 原始未转义片段不得出现在 SQL 中.
	if strings.Contains(sql, `TAGS('x\' OR 1=1--')`) {
		t.Fatalf("反斜杠绕过注入未被消除: %s", sql)
	}
	if !strings.Contains(sql, `TAGS('x\\\' OR 1=1--')`) {
		t.Fatalf("转义结果不符合预期: %s", sql)
	}
}
