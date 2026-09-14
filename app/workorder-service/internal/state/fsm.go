// Package state 定义工单生命周期状态机(FSM), 对齐 docs/M2设计文档V1.1 第八章确认方案.
// 确认状态(5态): 待派单(0)→处理中(1)→待验收(2)→已完成(3), 终态 已关闭(4).
// 动作: assign(派单) / submit(提交) / approve(验收通过) / reject(驳回) / close(关闭).
// 特殊流转: 驳回回到处理中; 任意活跃态可关闭; 建单即待派单(取消=关闭).
package state

// 工单状态码(与 WorkOrder.Status 字段含义一致)
const (
	StatusPendingDispatch int8 = 0 // 待派单: 建单即此态
	StatusProcessing      int8 = 1 // 处理中
	StatusPendingVerify   int8 = 2 // 待验收
	StatusCompleted       int8 = 3 // 已完成(正常终态)
	StatusClosed          int8 = 4 // 已关闭(终态, 可从任意活跃态关闭)
)

// 工单动作(与 UpdateWorkOrderStatusReq.Action 字段一致)
const (
	ActionAssign  = "assign"  // 派单: 待派单→处理中
	ActionSubmit  = "submit"  // 提交完成: 处理中→待验收
	ActionApprove = "approve" // 验收通过: 待验收→已完成
	ActionReject  = "reject"  // 驳回: 待验收→处理中
	ActionClose   = "close"   // 关闭: 任意活跃态→已关闭
)

// transitions 状态转移表: from -> action -> to.
// 已关闭(4)为终态, 无任何出边.
var transitions = map[int8]map[string]int8{
	StatusPendingDispatch: {ActionAssign: StatusProcessing, ActionClose: StatusClosed},
	StatusProcessing:      {ActionSubmit: StatusPendingVerify, ActionClose: StatusClosed},
	StatusPendingVerify:   {ActionApprove: StatusCompleted, ActionReject: StatusProcessing, ActionClose: StatusClosed},
	StatusCompleted:       {ActionClose: StatusClosed},
}

// CanTransition 判断从 from 状态执行 action 是否合法(用于入参校验).
func CanTransition(from int8, action string) bool {
	_, ok := transitions[from][action]
	return ok
}

// NextStatus 返回 from 状态执行 action 后的目标状态; 非法流转 ok=false.
func NextStatus(from int8, action string) (to int8, ok bool) {
	to, ok = transitions[from][action]
	return
}

// IsTerminal 判断状态是否为终态. 已完成(3)仍可被 close, 故仅已关闭(4)为终态.
func IsTerminal(status int8) bool {
	return status == StatusClosed
}

// IsValidStatus 判断状态码是否合法(防止脏数据/越权传参).
func IsValidStatus(status int8) bool {
	switch status {
	case StatusPendingDispatch, StatusProcessing, StatusPendingVerify, StatusCompleted, StatusClosed:
		return true
	}
	return false
}
