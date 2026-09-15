package logic

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"onepark/common/errorx"

	"onepark/app/energy-analysis-service/internal/ecode"
)

const (
	// timeLayoutSecond 完整时间, 如 2026-09-15 09:05:51
	timeLayoutSecond = "2006-01-02 15:04:05"
	// timeLayoutMinute 精确到分钟, 如 2026-09-15 09:05
	timeLayoutMinute = "2006-01-02 15:04"
	// timeLayoutDate 只有日期, 如 2026-09-15
	timeLayoutDate = "2006-01-02"
	// timeLayoutMonth 只有年月, 如 2026-09
	timeLayoutMonth = "2006-01"
)

// ParseDay 把日期字符串解析成"当天 0 点"和"次日 0 点"两个时刻
// 空字符串表示今天; 解析失败返回 false
func ParseDay(s string) (time.Time, time.Time, bool) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start, ok := parseTime(s, today)
	if !ok {
		return start, start, false
	}
	return start, start.AddDate(0, 0, 1), true
}

// ParseMonth 把月份字符串解析成"当月 1 号 0 点"和"次月 1 号 0 点"两个时刻
// 空字符串表示本月; 解析失败返回 false
func ParseMonth(s string) (time.Time, time.Time, bool) {
	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	start, ok := parseTime(s, first)
	if !ok {
		return start, start, false
	}
	return start, start.AddDate(0, 1, 0), true
}

// ParseRange 解析一个自定义时间范围, 两个都为空表示"今天一整天"
// 结束时间不传就默认到"开始时间的次日 0 点"; 两者都合法且 start < end 才返回 true
func ParseRange(startStr, endStr string) (time.Time, time.Time, bool) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	start, ok := parseTime(startStr, today)
	if !ok {
		return start, start, false
	}
	end, ok := parseTime(endStr, start.AddDate(0, 0, 1))
	if !ok {
		return start, end, false
	}
	if !end.After(start) {
		return start, end, false
	}
	return start, end, true
}

// parseTime 把用户传的时间字符串转成 time.Time, 支持四种写法:
//
//	2026-09              (只有年月)
//	2026-09-15           (只有日期)
//	2026-09-15 09:00     (到分钟)
//	2026-09-15 09:00:00  (到秒)
//
// 传空字符串就用默认值 def; 四种都解析不了就返回 false
func parseTime(s string, def time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, true
	}
	for _, layout := range []string{timeLayoutSecond, timeLayoutMinute, timeLayoutDate, timeLayoutMonth} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return def, false
}

// round2 保留两位小数, 免得返回 3.0000000000000004 这种数
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// percent 算 part 占 total 的百分比, total 为 0 时返回 0
func percent(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return round2(part / total * 100)
}

// wrapErr 把数据库错误统一转成对外错误码, 顺便打日志
func wrapErr(msg string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errorx.NewError(ecode.ErrZoneNoData, msg+"：暂无数据")
	}
	logx.Errorf("%s失败: %v", msg, err)
	return errorx.NewError(ecode.ErrQueryFailed, msg+"失败")
}
