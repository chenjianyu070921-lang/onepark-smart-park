// Package dispatch 承载 M3 → M5 的语义翻译: 告警等级 → 工单优先级映射.
//
// 为什么单独成包而不是塞进 notify: notify 只负责"把事件可靠地发出去",
// 而本包回答的是"这条告警在 M5 该排多急" —— 前者是传输关注点, 后者是业务口径,
// 两者的变更频率与验收方式都不同, 混在一起会让"改了优先级规则"看起来像"改了发送逻辑".
package dispatch

import (
	"sort"
	"strconv"
	"strings"

	"onepark/app/alarm-service/internal/model"
)

// 工单优先级取值, 与 M5 app/dispatch-service/internal/model/task.go 保持一致.
//
// ⚠️ 方向性(与 M5 对齐前最容易搞反的一点):
//   - M3 告警等级: 值越大越严重(1提示 2一般 3严重 4紧急);
//   - M5 工单优先级: 值越小越紧急(1紧急 2高 3普通)。
//
// 两者方向相反, 所以映射不能写成"透传"。M3 对外事件里 severity 仍按 M3 自身语义发送
// (见 notify.AlarmEvent.Severity 注释), priority 才用 M5 语义 —— 两个字段各带各的口径,
// 消费方不必再猜"这个数字是越大越严重还是越小越严重".
const (
	PriorityUrgent int8 = 1 // 紧急: 立即响应
	PriorityHigh   int8 = 2 // 高
	PriorityNormal int8 = 3 // 普通
)

// DefaultTable 默认映射表(M3 等级 → M5 优先级).
//
// 为什么"提示/一般"都落到普通: M5 只有三档优先级, 没有"低"这一档。
// 把"提示"编造出一个 4 会让 dispatch 的 validPriority(1/2/3) 校验直接拒单,
// 结果是低等级告警一条都派不出去 —— 宁可三档共用, 也不制造必然被拒的单据.
var DefaultTable = Table{
	model.AlarmLevelCritical: PriorityUrgent,
	model.AlarmLevelMajor:    PriorityHigh,
	model.AlarmLevelMinor:    PriorityNormal,
	model.AlarmLevelInfo:     PriorityNormal,
}

// Table 告警等级 → 工单优先级映射表. nil 值可用: 缺项回退 DefaultTable.
type Table map[int8]int8

// PriorityOf 返回该告警等级对应的工单优先级.
//
// 未知等级(0 或越界)一律回退 PriorityNormal 而不是返回错误:
// M5 对未知 event_type 的处理同样是"不丢弃、按普通建单"
// (dispatch-service/internal/consumer/alarm.go BuildTaskDraft),
// 让罕见取值在 M3 侧静默消失会变成"告警存在但没人被派去处理"的漏派单,
// 代价远高于"一条等级异常的告警按普通优先级处理".
func (t Table) PriorityOf(level int8) int8 {
	if p, ok := t[level]; ok {
		return p
	}
	if p, ok := DefaultTable[level]; ok {
		return p
	}
	return PriorityNormal
}

// ParseTable 将配置中的覆盖项合并到默认映射上, 返回最终映射与被拒绝的条目说明.
//
// 非法条目为什么不直接返回 error 终止: 这是一条旁路关注点 ——
// 运营把某条等级配错时, 更合理的行为是"其余等级照常生效 + 打日志提示配错的那些",
// 而不是整个映射失效、所有告警退化为默认;
// 但也不能静默忽略, 否则"配置写错了却一直按老规则派单"无从发现, 故返回原因由调用方记 WARN.
func ParseTable(overrides map[string]int) (Table, []string) {
	t := make(Table, len(DefaultTable))
	for level, priority := range DefaultTable {
		t[level] = priority
	}

	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	// 排序保证输出稳定: 同一份配置每次启动产生的 WARN 顺序一致, 便于比对日志.
	sort.Strings(keys)

	var rejected []string
	for _, k := range keys {
		level, err := strconv.Atoi(strings.TrimSpace(k))
		if err != nil || level < int(model.AlarmLevelInfo) || level > int(model.AlarmLevelCritical) {
			rejected = append(rejected, "告警等级键非法: "+k)
			continue
		}
		priority := overrides[k]
		if priority != int(PriorityUrgent) && priority != int(PriorityHigh) && priority != int(PriorityNormal) {
			rejected = append(rejected, "等级 "+k+" 的优先级取值 "+strconv.Itoa(priority)+" 非法(仅支持 1紧急/2高/3普通)")
			continue
		}
		t[int8(level)] = int8(priority)
	}
	return t, rejected
}
