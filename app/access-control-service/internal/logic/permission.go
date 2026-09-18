package logic

import (
	"encoding/json"
	"time"

	"onepark/app/access-control-service/internal/model"
)

// timeWindowJSON 时间段权限的库内结构, 与 types.TimeWindow 一一对应.
type timeWindowJSON struct {
	Start string  `json:"start"`
	End   string  `json:"end"`
	Days  []int64 `json:"days"`
}

// PermissionAllowed 判定一条授权在 at 时刻是否放行.
//
// 判定顺序(与 #45 授权时的校验口径一致):
//  1. 无授权记录 -> 不放行;
//  2. whitelist=1 -> 跳过时间段限制(但仍受过期时间约束);
//  3. expire_at 已过 -> 不放行;
//  4. time_window 为空 -> 全天放行;
//  5. time_window 非空 -> 需同时满足"星期几在 days 内"且"当前时刻落在 [start, end]".
//
// 返回 (是否放行, 不放行的原因); 原因用于写入通行记录的 fail_reason 与审计 message.
func PermissionAllowed(p *model.AccessPermission, at time.Time) (bool, string) {
	if p == nil {
		return false, "未查询到有效授权"
	}
	if p.Status != model.PermissionStatusValid {
		return false, "授权已失效"
	}
	if p.ExpireAt != nil && !at.Before(*p.ExpireAt) {
		return false, "授权已过期"
	}
	// 白名单只豁免时间段, 不豁免有效期: 白名单语义是"随时可进", 不是"永远可进".
	if p.Whitelist == 1 {
		return true, ""
	}
	if p.TimeWindow == nil || *p.TimeWindow == "" {
		return true, ""
	}
	var w timeWindowJSON
	if err := json.Unmarshal([]byte(*p.TimeWindow), &w); err != nil {
		// 库里的 JSON 坏了不能当成"无限制"放行 —— 那等于把坏数据变成一条永久通行证.
		return false, "授权时间段数据异常"
	}
	if !dayMatches(w.Days, at) {
		return false, "当前不在授权日期内"
	}
	minutes := at.Hour()*60 + at.Minute()
	start, err1 := parseWindowMinutes(w.Start)
	end, err2 := parseWindowMinutes(w.End)
	if err1 != nil || err2 != nil {
		return false, "授权时间段数据异常"
	}
	if minutes < start || minutes > end {
		return false, "当前不在授权时间段内"
	}
	return true, ""
}

// dayMatches 判定 at 是星期几是否落在 days 内(1=周一 ... 7=周日).
// Go 的 time.Weekday() 以周日=0 开头, 需换算成 1-7 才能与契约里的 days 对齐.
func dayMatches(days []int64, at time.Time) bool {
	if len(days) == 0 {
		return true
	}
	wd := int(at.Weekday())
	if wd == 0 {
		wd = 7
	}
	for _, d := range days {
		if int(d) == wd {
			return true
		}
	}
	return false
}

// parseWindowMinutes 把 "HH:mm" 转为当日起的分钟数.
func parseWindowMinutes(v string) (int, error) {
	t, err := parseHHMM(v)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}
