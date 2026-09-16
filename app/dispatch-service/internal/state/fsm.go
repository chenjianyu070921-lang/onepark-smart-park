// Package state 定义调度工单生命周期状态机(FSM).
//
// 状态流转: 待指派 → 已指派 → 处理中 → 已完成
// 任意非终态均可「关闭」; 已关闭为终态, 不可逆.
package state

import "onepark/app/dispatch-service/internal/model"

// 工单动作.
const (
	ActionCreate = "create" // 建单: -> 待指派
	ActionAssign = "assign" // 指派: 待指派/已指派 -> 已指派
	ActionStart  = "start"  // 开始处理: 已指派 -> 处理中
	ActionFinish = "finish" // 完成: 处理中 -> 已完成
	ActionClose  = "close"  // 关闭: 任意非终态 -> 已关闭
)

// transitions 合法状态转移表: from -> action -> to.
var transitions = map[int8]map[string]int8{
	model.StatusPendingAssign: {
		ActionAssign: model.StatusAssigned,
		ActionClose:  model.StatusClosed,
	},
	model.StatusAssigned: {
		ActionAssign: model.StatusAssigned, // 改派
		ActionStart:  model.StatusProcessing,
		ActionClose:  model.StatusClosed,
	},
	model.StatusProcessing: {
		ActionFinish: model.StatusCompleted,
		ActionClose:  model.StatusClosed,
	},
	model.StatusCompleted: {
		ActionClose: model.StatusClosed, // 完成后归档
	},
	// 已关闭为终态, 无任何出边
}

// Next 返回状态转移后的目标状态; ok=false 表示该转移非法.
func Next(from int8, action string) (to int8, ok bool) {
	to, ok = transitions[from][action]
	return
}

// IsTerminal 判断是否为终态.
func IsTerminal(status int8) bool {
	return status == model.StatusClosed
}

// IsValid 判断状态码是否合法.
func IsValid(status int8) bool {
	switch status {
	case model.StatusPendingAssign, model.StatusAssigned, model.StatusProcessing,
		model.StatusCompleted, model.StatusClosed:
		return true
	}
	return false
}
