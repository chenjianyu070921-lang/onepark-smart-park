package logic

import (
	"testing"
	"time"

	"onepark/app/video-service/internal/model"
)

// 时间锚点: 2026-09-21(周一) 10:00:00, 用于按工作日/自然日推导的确定性断言.
func anchor(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local)
}

func alwaysPlan(id int64) *model.RecordPlan {
	return &model.RecordPlan{
		ID: id, Name: "全天计划", Strategy: model.RecordStrategyAlways,
		Status: model.RecordPlanStatusEnabled, RetentionDays: 7,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
	}
}

func scheduledPlan(id int64, days string, startMinute, endMinute int) *model.RecordPlan {
	return &model.RecordPlan{
		ID: id, Name: "定时计划", Strategy: model.RecordStrategyScheduled, DaysOfWeek: days,
		StartMinute: startMinute, EndMinute: endMinute, Status: model.RecordPlanStatusEnabled,
		RetentionDays: 7, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
	}
}

// TestPlanWindows_NeverExceedsQueryRange 结果必须被查询区间裁剪:
// 全天计划在库里表达的是"永不停止", 若不做裁剪, 一次 1 小时的查询会返回从零点起的一整天.
func TestPlanWindows_NeverExceedsQueryRange(t *testing.T) {
	now := anchor(t)
	from := now.Add(-30 * time.Minute)
	to := now

	got := availableWindows([]*model.RecordPlan{alwaysPlan(1)}, from, to, now)
	if len(got) != 1 {
		t.Fatalf("应返回 1 段窗口, 实际 %d", len(got))
	}
	if !got[0].start.Equal(from) || !got[0].end.Equal(to) {
		t.Errorf("窗口应与查询区间一致: got [%v,%v) want [%v,%v)", got[0].start, got[0].end, from, to)
	}
}

// TestPlanWindows_ScheduledWeeklyWindow 定时计划只在生效日的时段内产出窗口.
// 锚点是周一: 工作日计划(1-5)应命中, 周末计划(6-7)不应命中.
func TestPlanWindows_ScheduledWeeklyWindow(t *testing.T) {
	now := anchor(t)
	from := time.Date(2026, 9, 21, 8, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 21, 20, 0, 0, 0, time.Local)

	workday := availableWindows([]*model.RecordPlan{scheduledPlan(1, "1,2,3,4,5", 9*60, 18*60)}, from, to, now)
	if len(workday) != 1 {
		t.Fatalf("工作日 9:00-18:00 应命中一段, 实际 %d", len(workday))
	}
	if workday[0].start.Hour() != 9 || workday[0].end.Hour() != 18 {
		t.Errorf("窗口应为 9:00-18:00, 实际 %v-%v", workday[0].start, workday[0].end)
	}

	weekend := availableWindows([]*model.RecordPlan{scheduledPlan(2, "6,7", 9*60, 18*60)}, from, to, now)
	if len(weekend) != 0 {
		t.Errorf("周一的查询不应命中周末计划: %+v", weekend)
	}
}

// TestPlanWindows_MergesAdjacentDays 跨天查询按自然日切成多段后必须合并,
// 否则"全天录像"会被按天切成一堆碎片, 前端看起来像录像断断续续.
func TestPlanWindows_MergesAdjacentDays(t *testing.T) {
	now := anchor(t)
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)

	got := availableWindows([]*model.RecordPlan{alwaysPlan(1)}, from, to, now)
	if len(got) != 1 {
		t.Fatalf("连续 3 天的全天录像应合并为 1 段, 实际 %d 段: %+v", len(got), got)
	}
	if !got[0].start.Equal(from) || !got[0].end.Equal(to) {
		t.Errorf("合并后应覆盖完整区间: [%v,%v)", got[0].start, got[0].end)
	}
}

// TestPlanWindows_RetentionTrimsExpiredTail 超出保留期的部分必须从窗口里剔除:
// 只保留 2 天却返回 7 天前的录像, 前端会给出看似可播的链接, 真播时才发现文件没了.
func TestPlanWindows_RetentionTrimsExpiredTail(t *testing.T) {
	now := anchor(t)
	plan := alwaysPlan(1)
	plan.RetentionDays = 2

	got := availableWindows([]*model.RecordPlan{plan}, now.Add(-72*time.Hour), now, now)
	if len(got) != 1 {
		t.Fatalf("应返回裁剪后的 1 段窗口, 实际 %d", len(got))
	}
	wantFloor := now.AddDate(0, 0, -2)
	if got[0].start.Before(wantFloor) || got[0].start.Sub(wantFloor) > time.Second {
		t.Errorf("起始应为保留期底线 %v, 实际 %v", wantFloor, got[0].start)
	}
	if !got[0].end.Equal(now) {
		t.Errorf("结束应保持为查询上界 %v, 实际 %v", now, got[0].end)
	}
}

// TestPlanWindows_FullyExpiredDropped 全部超出保留期时返回空, 而不是返回负长度窗口.
func TestPlanWindows_FullyExpiredDropped(t *testing.T) {
	now := anchor(t)
	plan := alwaysPlan(1)
	plan.RetentionDays = 1

	got := availableWindows([]*model.RecordPlan{plan}, now.Add(-72*time.Hour), now.Add(-48*time.Hour), now)
	if len(got) != 0 {
		t.Errorf("全部过期的窗口应被丢弃, 实际 %+v", got)
	}
}

// TestPlanWindows_CreatedAtFloor 计划创建之前不应有窗口 ——
// 昨天才配的计划不能给出上个月"按这个计划"产生的录像.
func TestPlanWindows_CreatedAtFloor(t *testing.T) {
	now := anchor(t)
	plan := alwaysPlan(1)
	plan.CreatedAt = now.Add(-24 * time.Hour) // 昨天才创建
	plan.RetentionDays = 30

	got := availableWindows([]*model.RecordPlan{plan}, now.Add(-72*time.Hour), now, now)
	if len(got) != 1 {
		t.Fatalf("应返回被创建时间裁剪后的 1 段, 实际 %d", len(got))
	}
	if got[0].start.Before(plan.CreatedAt) {
		t.Errorf("窗口起点不得早于计划创建时间 %v, 实际 %v", plan.CreatedAt, got[0].start)
	}
}

// TestPlanWindows_UnusablePlansIgnored 停用计划 / 非法生效日 / 跨零点写法都不产出窗口,
// 且不影响同一批里其它计划的有效性(一条脏数据不该拖垮整次回放查询).
func TestPlanWindows_UnusablePlansIgnored(t *testing.T) {
	now := anchor(t)
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)

	disabled := alwaysPlan(1)
	disabled.Status = model.RecordPlanStatusDisabled
	badDays := scheduledPlan(2, "9", 9*60, 18*60)      // 生效日越界
	crossMidnight := scheduledPlan(3, "1", 22*60, 6*60) // 跨零点, 约定拆两条

	got := availableWindows([]*model.RecordPlan{disabled, badDays, crossMidnight, alwaysPlan(4)}, from, to, now)
	if len(got) != 1 {
		t.Fatalf("三条不可用计划应各跳过, 只剩 1 段, 实际 %d", len(got))
	}
	if got[0].planID != 4 {
		t.Errorf("应只剩第 4 条计划, 实际 plan_id=%d", got[0].planID)
	}
}

// TestPlanWindows_MultiplePlansMerged 多条计划重叠时合并, 归属取开始时间最早的那条(结果可复现).
func TestPlanWindows_MultiplePlansMerged(t *testing.T) {
	now := anchor(t)
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)

	morning := scheduledPlan(1, "", 0, 12*60)
	afternoon := scheduledPlan(2, "", 10*60, 24*60)

	got := availableWindows([]*model.RecordPlan{afternoon, morning}, from, to, now)
	if len(got) != 1 {
		t.Fatalf("重叠的两段应合并成一段, 实际 %d", len(got))
	}
	if got[0].start.Hour() != 0 || got[0].end.Sub(from) != 24*time.Hour {
		t.Errorf("合并后应覆盖 0:00-24:00, 实际 [%v,%v)", got[0].start, got[0].end)
	}
	if got[0].planID != 1 {
		t.Errorf("合并段应归属开始最早的第 1 条计划, 实际 %d", got[0].planID)
	}
}

// TestMergeWindows_GapKeptSeparate 非相邻窗口不得被强行拼接:
// 把"没录的时间"塞进回放区间等于谎报录像覆盖范围.
func TestMergeWindows_GapKeptSeparate(t *testing.T) {
	base := anchor(t)
	in := []window{
		{start: base, end: base.Add(time.Hour), planID: 1},
		{start: base.Add(2 * time.Hour), end: base.Add(3 * time.Hour), planID: 2},
	}
	got := mergeWindows(in)
	if len(got) != 2 {
		t.Fatalf("中间有空隙的两段应保持分开, 实际 %d", len(got))
	}
}
