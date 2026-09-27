package model

import "testing"

// TestEscapeLike 转义 LIKE 通配符: 用户输入的 % 与 _ 不得改变匹配语义.
//
// 攻击面不是 SQL 注入(参数化已挡住), 而是**语义劫持**: 用户输入 "%" 若不转义,
// 会变成 "LIKE '%%%'" 匹配所有行 —— 用户以为在搜关键词, 实际返回了全量,
// 且不会被任何人发现(接口返回 200、列表看起来"正常")。
func TestEscapeLike(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"普通中文":       {in: "温度过高", want: "温度过高"},
		"百分号":        {in: "50%", want: `50\%`},
		"下划线":        {in: "a_b", want: `a\_b`},
		"反斜杠自身":      {in: `\`, want: `\\`},
		"混合":         {in: `100%_x\y`, want: `100\%\_x\\y`},
		"空串":         {in: "", want: ""},
		"只有通配符":      {in: "%_", want: `\%\_`},
		"已转义需再加一层":   {in: `\%`, want: `\\\%`},
	}
	for name, c := range cases {
		if got := escapeLike(c.in); got != c.want {
			t.Errorf("%s: escapeLike(%q) = %q, 期望 %q", name, c.in, got, c.want)
		}
	}
}

// TestEscapeLike_IdempotentLikePattern 转义后的串配合 ESCAPE '\' 应能被正确还原为字面量.
// 这里验证的是"顺序正确": 必须先转义反斜杠, 否则 \% 会二次变成 \\% .
func TestEscapeLike_IdempotentLikePattern(t *testing.T) {
	got := escapeLike(`a%b`)
	// 期望: a\%b —— 反斜杠是转义符, % 是字面量.
	if got != `a\%b` {
		t.Fatalf("escapeLike(a%%b) = %q, 期望 a\\%%b", got)
	}
	// 再转义一次不能把已转义的 \% 变成"字面反斜杠 + 通配符".
	if again := escapeLike(got); again != `a\\\%b` {
		t.Errorf("二次转义 = %q, 期望 a\\\\\\%%b(每层都独立转义)", again)
	}
}
