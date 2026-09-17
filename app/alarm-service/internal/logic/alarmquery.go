package logic

import (
	"strconv"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
)

// pageLimit 页大小上限, 与 model.normalizePage / search.normalizePage 保持一致.
const pageLimit = 100

// normalizePaging 归一化分页参数.
// 必须与存储层用同一套规则: 否则响应回显的是入参(如 page_size=1000), 实际下推的是 100,
// 前端按回显值算总页数会得到错误的页数.
func normalizePaging(page, size int64) (int64, int64) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > pageLimit {
		size = pageLimit
	}
	return page, size
}

// validateLevel 校验告警等级筛选值: <=0 表示不筛选, 1~4 为合法档位, 越界返回参数错误.
// 越界必须报错而不是返回空列表 —— 空列表会把调用方的笔误(level=99)伪装成"没有数据".
func validateLevel(level int8) (*int8, error) {
	if level <= 0 {
		return nil, nil
	}
	// 下界已被上面的 <=0 分支排除, 这里只需校验上界.
	// 不要写成 level < AlarmLevelInfo || level > AlarmLevelCritical: 前半个条件恒为 false,
	// 静态检查会报"条件始终为 false", 也会让后来读代码的人以为负数会走到这里.
	if level > model.AlarmLevelCritical {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "level 取值范围为 1(提示)~4(紧急)")
	}
	lv := level
	return &lv, nil
}

// timeRange 将秒级时间戳入参转为时间指针; 0 表示不限.
// 区间反转(start > end)直接判参数非法: 否则查询必定为空, 调用方会误判为"该时段无告警".
func timeRange(start, end int64) (*time.Time, *time.Time, error) {
	var st, et *time.Time
	if start > 0 {
		t := time.Unix(start, 0)
		st = &t
	}
	if end > 0 {
		t := time.Unix(end, 0)
		et = &t
	}
	if st != nil && et != nil && st.After(*et) {
		return nil, nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "start_time 不能晚于 end_time")
	}
	return st, et, nil
}

// alarmItems 将存储层告警批量转换为接口返回项.
func alarmItems(list []*model.Alarm) []types.AlarmItem {
	items := make([]types.AlarmItem, 0, len(list))
	for _, a := range list {
		items = append(items, types.AlarmItem{
			Id:        a.ID,
			AlarmNo:   a.AlarmNo,
			DeviceId:  a.DeviceID,
			AreaId:    a.AreaID,
			EventType: a.EventType,
			Level:     a.Level,
			Status:    a.Status,
			Content:   a.Content,
			CreatedAt: a.CreatedAt.Unix(),
		})
	}
	return items
}

// levelAgg 组装等级分布聚合(docs/m3/04 #41).
// 无数据时返回空 map 而非 nil: 前端不必区分"没有聚合"与"聚合为空"两种情况.
func levelAgg(counts []model.LevelCount) *types.AlarmLevelAgg {
	agg := &types.AlarmLevelAgg{LevelCount: make(map[string]int64, len(counts))}
	for _, c := range counts {
		agg.LevelCount[levelKey(c.Level)] = c.Total
	}
	return agg
}

// levelKey 等级转聚合键. JSON 对象的键只能是字符串, 统一在此转换避免各调用点各写一遍.
func levelKey(level int8) string {
	return strconv.FormatInt(int64(level), 10)
}
