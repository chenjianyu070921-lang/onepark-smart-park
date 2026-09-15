// Package state 定义合同生命周期状态机(FSM).
//
// 设计要点:
//   - 「已到期」(自然到期) 与「已终止」(提前解约) 是两个不同语义的终态,
//     不把二者串成一条必经路径, 避免"自然到期还要人工点一下才终止"。
//   - 「已终止」为终态, 不可逆 —— 已终止合同不复活, 重新出租请新建合同。
//   - 续签采用「延长原合同租期」语义(与接口清单 #66「续签日期延长」一致):
//     生效中续签保持生效中; 已到期续签则重新生效。
package state

import "onepark/app/leasing-service/internal/model"

// 合同动作.
const (
	ActionCreate    = "create"    // 建单: -> 待生效
	ActionActivate  = "activate"  // 待生效 -> 生效中
	ActionExpire    = "expire"    // 生效中 -> 已到期
	ActionTerminate = "terminate" // 待生效/生效中/已到期 -> 已终止
	ActionRenew     = "renew"     // 续签: 延长租期
)

// transitions 合法状态转移表: from -> action -> to.
var transitions = map[int8]map[string]int8{
	model.StatusPending: {
		ActionActivate:  model.StatusActive,
		ActionTerminate: model.StatusTerminated,
	},
	model.StatusActive: {
		ActionRenew:     model.StatusActive, // 续签: 延长租期(可调租金), 状态保持生效中
		ActionExpire:    model.StatusExpired,
		ActionTerminate: model.StatusTerminated,
	},
	model.StatusExpired: {
		ActionRenew:     model.StatusActive, // 到期续签: 重新生效并延长租期
		ActionTerminate: model.StatusTerminated,
	},
	// 已终止为终态, 无任何出边
}

// Next 返回状态转移后的目标状态; ok=false 表示该转移非法.
func Next(from int8, action string) (to int8, ok bool) {
	to, ok = transitions[from][action]
	return
}

// IsTerminal 判断是否为终态. 仅「已终止」为终态; 「已到期」仍可流转到已终止(清退归档).
func IsTerminal(status int8) bool {
	return status == model.StatusTerminated
}

// IsValid 判断状态码是否合法(防脏数据).
func IsValid(status int8) bool {
	switch status {
	case model.StatusPending, model.StatusActive, model.StatusExpired, model.StatusTerminated:
		return true
	}
	return false
}
