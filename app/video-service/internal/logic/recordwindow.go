package logic

import (
	"sort"
	"time"

	"onepark/app/video-service/internal/model"
)

// maxPlanWindowDays 单次窗口推导允许遍历的最大天数(防御性上限).
// 正常查询已被 Record.MaxRangeHours 限制在 24h 内, 这里只是给"按天遍历"兜个上限:
// 万一配置的跨度上限改大或实现被复用到别的入口, 也不会因为一个超长区间把请求拖成死循环.
const maxPlanWindowDays = 400

// window 一段可用录像窗口, 携带产出它的计划信息以便前端标注"按哪条计划录的".
type window struct {
	start    time.Time
	end      time.Time
	planID   int64
	planName string
}

// availableWindows 推导一组计划在 [from, to) 内的可用录像窗口.
//
// 裁剪顺序有意义, 三条约束缺一不可:
//  1. 与查询区间求交 —— 结果永不超过调用方请求的范围;
//  2. 被计划创建时间裁剪 —— 计划创建之前不存在"按这个计划产生的录像";
//  3. 被保留期裁剪 —— now-retention 之前的录像视为已过期, 不可回放。
//
// 其中 2 与 3 取**较晚者**作为下界: 只取其一会让"新建计划查很久以前"漏掉创建时间这条约束,
// 或让"计划很老但录像已过期"错误地返回还能看。
//
// 另有一条隐含约束: 录像不可能来自未来, 上界 to 由调用方裁剪到当前时刻, 本函数不重复处理。
func availableWindows(plans []*model.RecordPlan, from, to, now time.Time) []window {
	out := make([]window, 0, len(plans))
	for _, p := range plans {
		out = append(out, planWindows(p, from, to, now)...)
	}
	return mergeWindows(out)
}

// planWindows 推导单条计划的录像窗口.
//
// 对"不可解释的计划"一律返回空窗口而不是报错: 一次回放查询里可能有若干条计划,
// 一条脏数据(配置文件被手改成非法值、跨零点写法)不该让其它计划的有效窗口一起消失。
func planWindows(p *model.RecordPlan, from, to, now time.Time) []window {
	if p == nil || p.Status != model.RecordPlanStatusEnabled || !to.After(from) {
		return nil
	}

	retention := p.RetentionDays
	if retention <= 0 {
		retention = defaultRetentionDays
	}
	// 下界: 计划创建时间与保留期底线取较晚者.
	floor := now.AddDate(0, 0, -retention)
	if p.CreatedAt.After(floor) {
		floor = p.CreatedAt
	}

	scheduled := p.Strategy == model.RecordStrategyScheduled
	days, err := model.ParseDaysOfWeek(p.DaysOfWeek)
	if scheduled && err != nil {
		return nil
	}
	startMinute, endMinute := model.NormalizeMinutes(p.StartMinute, p.EndMinute)
	if scheduled && endMinute <= startMinute {
		// 跨零点(如 22:00-06:00)不支持: 它到底属于哪一天没有唯一的解释口径,
		// 强行拆分再合并只会让"这段有没有录"说不清楚. 约定拆成两条计划.
		return nil
	}

	loc := from.Location()
	out := make([]window, 0, 4)
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < maxPlanWindowDays && !day.After(to); i++ {
		dayStart, dayEnd := day, day.AddDate(0, 0, 1)
		if scheduled {
			if len(days) > 0 && !days[day.Weekday()] {
				day = day.AddDate(0, 0, 1)
				continue
			}
			dayStart = day.Add(time.Duration(startMinute) * time.Minute)
			dayEnd = day.Add(time.Duration(endMinute) * time.Minute)
		}

		if w, ok := clipWindow(dayStart, dayEnd, from, to, floor); ok {
			out = append(out, window{start: w[0], end: w[1], planID: p.ID, planName: p.Name})
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}

// clipWindow 把 [ws, we) 依次与查询区间求交、按下界裁剪; 返回裁剪后的窗口与是否仍有效.
// 任一环节让窗口退化为空(左边界不再早于右边界)就返回 false, 由调用方丢弃该窗口.
func clipWindow(ws, we, from, to, floor time.Time) ([2]time.Time, bool) {
	if ws.Before(from) {
		ws = from
	}
	if we.After(to) {
		we = to
	}
	if !ws.Before(we) {
		return [2]time.Time{}, false
	}
	if ws.Before(floor) {
		ws = floor
	}
	if !ws.Before(we) {
		return [2]time.Time{}, false
	}
	return [2]time.Time{ws, we}, true
}

// mergeWindows 合并重叠/相邻的窗口并按开始时间升序排列.
//
// 为什么要合并: 同一摄像头配了多条计划时(例如"工作日 8:00-20:00" + "全天低码率"),
// 逐条返回会让前端拿到一堆互相叠在一起的分段, 既重复请求也看不出实际覆盖范围。
// 相邻(前一段结束 == 后一段开始)也合并, 否则"跨天全天录像"会被切成每天一段。
//
// 合并后的窗口归属最早的那条计划: 归属信息只用于标注来源, 多计划重叠时保留哪条都说得通,
// 这里固定取最早的一条以保证结果稳定可复现。
func mergeWindows(in []window) []window {
	if len(in) <= 1 {
		return in
	}
	sorted := make([]window, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].start.Equal(sorted[j].start) {
			return sorted[i].start.Before(sorted[j].start)
		}
		return sorted[i].planID < sorted[j].planID
	})

	out := make([]window, 0, len(sorted))
	out = append(out, sorted[0])
	for _, w := range sorted[1:] {
		last := &out[len(out)-1]
		if !w.start.After(last.end) {
			if w.end.After(last.end) {
				last.end = w.end
			}
			continue
		}
		out = append(out, w)
	}
	return out
}
