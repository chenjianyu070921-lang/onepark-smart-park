package logic

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"onepark/common/errorx"

	"onepark/app/billing-service/internal/ecode"
)

const (
	// timeLayoutMonth 只有年月, 如 2026-09
	timeLayoutMonth = "2006-01"
	// timeLayoutDate 只有日期, 如 2026-09-15
	timeLayoutDate = "2006-01-02"
)

// ParsePeriod 把账期字符串解析成"当月 1 号 0 点"和"次月 1 号 0 点"
// 空字符串表示本月; 解析失败返回 false
func ParsePeriod(s string) (time.Time, time.Time, bool) {
	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	s = strings.TrimSpace(s)
	if s == "" {
		return first, first.AddDate(0, 1, 0), true
	}
	t, err := time.ParseInLocation(timeLayoutMonth, s, time.Local)
	if err != nil {
		return first, first, false
	}
	return t, t.AddDate(0, 1, 0), true
}

// LastDay 账期的最后一天: 数据库里 period_end 存的是"含当天", 所以要往前推一天
// 例: 9 月的账期是 9/1 ~ 9/30, 而不是 9/1 ~ 10/1
func LastDay(end time.Time) time.Time {
	return end.AddDate(0, 0, -1)
}

// StatusOf 把状态参数转成数字, 不传返回 -1 表示"不限"
func StatusOf(s string) int64 {
	switch strings.TrimSpace(s) {
	case "":
		return -1
	case "1":
		return 1
	case "2":
		return 2
	case "3":
		return 3
	case "0":
		return 0
	default:
		return -1
	}
}

// round2 保留两位小数
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// wrapErr 把数据库错误统一转成对外错误码, 顺便打日志
func wrapErr(msg string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errorx.NewError(ecode.ErrRuleNotFound, msg+"：记录不存在")
	}
	logx.Errorf("%s失败: %v", msg, err)
	return errorx.NewError(ecode.ErrQueryFailed, msg+"失败")
}

// marshalJSON 序列化成 JSON 字符串, 失败返回 "{}" 而不是报错, 免得一条脏数据把整个流程搞挂
func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
