package logic

import (
	"strings"
	"time"
)

const (
	// timeLayoutSecond 完整时间, 如 2026-09-15 09:05:51
	timeLayoutSecond = "2006-01-02 15:04:05"
	// timeLayoutMinute 精确到分钟, 如 2026-09-15 09:05
	timeLayoutMinute = "2006-01-02 15:04"
	// timeLayoutDate 只有日期, 如 2026-09-15
	timeLayoutDate = "2006-01-02"

	// mysqlLayoutHour 按小时汇总时传给 MySQL DATE_FORMAT 的格式
	mysqlLayoutHour = "%Y-%m-%d %H:00"
	// mysqlLayoutDay 按天汇总时传给 MySQL DATE_FORMAT 的格式
	mysqlLayoutDay = "%Y-%m-%d"
)

// ParseDay 把日期字符串解析成"当天 0 点"和"次日 0 点"两个时刻
// 空字符串表示今天; 解析失败返回 false
func ParseDay(s string) (time.Time, time.Time, bool) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start, ok := parseTime(s, todayStart)
	if !ok {
		return start, start, false
	}
	return start, start.AddDate(0, 0, 1), true
}

// parseTime 把用户传的时间字符串转成 time.Time, 支持三种写法:
//
//	2026-09-15            (只有日期)
//	2026-09-15 09:00      (到分钟)
//	2026-09-15 09:00:00   (到秒)
//
// 传空字符串就用默认值 def; 三种都解析不了就返回 false
func parseTime(s string, def time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, true
	}
	for _, layout := range []string{timeLayoutSecond, timeLayoutMinute, timeLayoutDate} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return def, false
}
