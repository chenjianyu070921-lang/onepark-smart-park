package model

import (
	"testing"
	"time"
)

// TestParseDaysOfWeek_Semantics 生效日的解析与格式化: 1=周一 ... 7=周日, 空表示每天.
func TestParseDaysOfWeek_Semantics(t *testing.T) {
	cases := map[string]struct {
		in    string
		want  map[time.Weekday]bool
		isErr bool
	}{
		"空串=每天":  {in: "", want: map[time.Weekday]bool{}},
		"星号=每天":  {in: "*", want: map[time.Weekday]bool{}},
		"工作日":    {in: "1,2,3,4,5", want: map[time.Weekday]bool{time.Monday: true, time.Tuesday: true, time.Wednesday: true, time.Thursday: true, time.Friday: true}},
		"周日":     {in: "7", want: map[time.Weekday]bool{time.Sunday: true}},
		"带空格":    {in: " 1 , 6 ", want: map[time.Weekday]bool{time.Monday: true, time.Saturday: true}},
		"越界0":    {in: "0", isErr: true},
		"越界8":    {in: "8", isErr: true},
		"非数字":    {in: "mon", isErr: true},
		"含非法项":   {in: "1,9", isErr: true},
	}
	for name, c := range cases {
		got, err := ParseDaysOfWeek(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("%s: 应解析失败", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 不应失败: %v", name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: 生效日数量应为 %d, 实际 %d", name, len(c.want), len(got))
			continue
		}
		for day := range c.want {
			if !got[day] {
				t.Errorf("%s: 缺少生效日 %v", name, day)
			}
		}
	}
}

// TestFormatDaysOfWeek_Normalized 不同书写顺序归一为同一份文本,
// 否则 "1,2" 与 "2,1" 在列表里会被误读成两条不同的配置.
func TestFormatDaysOfWeek_Normalized(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"无生效日":  {in: "", want: ""},
		"乱序去重":  {in: "5,1,1,3", want: "1,3,5"},
		"周日排最后": {in: "7,1", want: "1,7"},
		"仅周日":   {in: "7", want: "7"},
	}
	for name, c := range cases {
		parsed, err := ParseDaysOfWeek(c.in)
		if err != nil {
			t.Errorf("%s: 解析失败: %v", name, err)
			continue
		}
		if got := FormatDaysOfWeek(parsed); got != c.want {
			t.Errorf("%s: 归一化应为 %q, 实际 %q", name, c.want, got)
		}
	}
}

// TestNormalizeMinutes_Clamped 越界的起止分钟被夹到 [0,1440], 不再抛错
// (真正的硬约束"开始早于结束"由 logic 层判定, 因为那条矛盾静默修好反而说不清).
func TestNormalizeMinutes_Clamped(t *testing.T) {
	cases := []struct {
		inStart, inEnd int
		wantStart, wantEnd int
	}{
		{inStart: -10, inEnd: 1500, wantStart: 0, wantEnd: 1440},
		{inStart: 600, inEnd: 1080, wantStart: 600, wantEnd: 1080},
		{inStart: 0, inEnd: 0, wantStart: 0, wantEnd: 0},
	}
	for _, c := range cases {
		gotStart, gotEnd := NormalizeMinutes(c.inStart, c.inEnd)
		if gotStart != c.wantStart || gotEnd != c.wantEnd {
			t.Errorf("NormalizeMinutes(%d,%d) = (%d,%d), 期望 (%d,%d)",
				c.inStart, c.inEnd, gotStart, gotEnd, c.wantStart, c.wantEnd)
		}
	}
}
