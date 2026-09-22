// Package state 定义调度工单生命周期状态机(FSM).
//
// 状态流转: 待指派 → 已指派 → 处理中 → 已完成
// 任意非终态均可「关闭」; 已关闭为终态, 不可逆.
package state

import "onepark/app/dispatch-service/internal/model"

// 工单动作.
const (
	ActionCreate = "create" // 建单: -> 待指派
	ActionAssign = "assign" // 指派: 待指派/已指派 -> 已指派(含超时自动改派)
	// ActionRelease 释放: 已指派 -> 待指派。
	// 用于「指派超时且无人可接管」或「重派次数达上限」——把工单放回待指派池交人工处理。
	// 为什么需要它: 若一直挂在「已指派」, 工单会永久卡住且不会出现在任何待办列表里。
	ActionRelease = "release"
	ActionStart   = "start"  // 开始处理: 已指派 -> 处理中
	ActionFinish  = "finish" // 完成: 处理中 -> 已完成
	ActionClose   = "close"  // 关闭: 任意非终态 -> 已关闭
	// ⚠️ 这里**刻意没有**独立的"超时退回"动作。
	//   工单的超时回收走 ActionRelease(已指派 -> 待指派), 由 cron/reassign.go 承担
	//   (改派他人; 无人可派或达上限时释放回池)。若再加一个语义相同的动作,
	//   转移表就会出现**两条通往同一状态的边**, 后人无从判断该用哪条。
	//
	//   (2026-09-21 删除: 原先这里有一个 ActionExpire + 一个从未被实例化的 ExpireScanner,
	//    职责与本条完全重合且绕过状态机直接 UPDATE status, 已一并移除。)
)

// transitions 合法状态转移表: from -> action -> to.
var transitions = map[int8]map[string]int8{
	model.StatusPendingAssign: {
		ActionAssign: model.StatusAssigned,
		ActionClose:  model.StatusClosed,
	},
	model.StatusAssigned: {
		ActionAssign:  model.StatusAssigned,      // 改派(含超时自动重派)
		ActionRelease: model.StatusPendingAssign, // 释放回待指派池(见 ActionRelease 注释)
		ActionStart:   model.StatusProcessing,
		ActionClose:   model.StatusClosed,
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
