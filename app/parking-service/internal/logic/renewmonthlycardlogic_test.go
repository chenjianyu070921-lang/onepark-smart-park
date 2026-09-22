package logic

import (
	"testing"
	"time"
)

// TestRenewEndValid 月卡续费边界(RenewMonthlyCard 业务约束, 对齐看板"月卡续费/过期"场景):
// 新到期时间必须晚于当前时间, 且晚于现有到期时间(防止"续费"反而缩短).
func TestRenewEndValid(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local)
	curEnd := now.Add(30 * 24 * time.Hour) // 未过期月卡
	newEnd := curEnd.Add(30 * 24 * time.Hour)

	// 正常续费: 未来时间续到更远未来.
	if !renewEndAfterNow(newEnd, now) || !renewEndAfterCur(newEnd, curEnd) {
		t.Error("正常续费(未来→更远未来)应合法")
	}
	// 续费到过去/当前时刻: 非法.
	if renewEndAfterNow(now.Add(-time.Hour), now) {
		t.Error("新到期时间早于当前时间应非法")
	}
	if renewEndAfterNow(now, now) {
		t.Error("新到期时间等于当前时间应非法")
	}
	// 续费"缩短": 新到期时间早于/等于现有到期时间, 非法.
	if renewEndAfterCur(curEnd.Add(-24*time.Hour), curEnd) {
		t.Error("新到期时间早于现有到期时间(缩短)应非法")
	}
	if renewEndAfterCur(curEnd, curEnd) {
		t.Error("新到期时间等于现有到期时间应非法")
	}
	// 过期卡续费: 现有到期时间在过去, 续到未来即合法(续费后自动恢复生效).
	expiredEnd := now.Add(-24 * time.Hour)
	if !renewEndAfterCur(now.Add(30*24*time.Hour), expiredEnd) {
		t.Error("过期卡续费到未来应合法")
	}
	if renewEndAfterNow(expiredEnd, now) {
		t.Error("把月卡续到仍过期的时点应非法")
	}
}
