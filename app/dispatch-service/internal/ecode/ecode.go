// Package ecode 定义 M5 指挥调度模块的错误码
// 格式: {Module}-{Level}-{Code}, 见 common/errorx/codes.go
package ecode

const (
	// 2001~2008 调度工单业务
	// ErrTaskNotFound 调度工单不存在
	ErrTaskNotFound = "M5-E-2001"
	// ErrTaskCreateFailed 调度工单创建失败
	ErrTaskCreateFailed = "M5-E-2002"
	// ErrTaskUpdateFailed 调度工单更新失败
	ErrTaskUpdateFailed = "M5-E-2003"
	// ErrTaskStatusInvalid 调度工单状态不允许该操作
	ErrTaskStatusInvalid = "M5-E-2004"
	// ErrAssigneeLoadFailed 候选处理人加载失败
	ErrAssigneeLoadFailed = "M5-E-2005"
	// ErrAssignFailed 指派失败
	ErrAssignFailed = "M5-E-2006"
	// ErrNoAssignee 暂无可用处理人
	ErrNoAssignee = "M5-E-2007"
	// ErrTaskConflict 调度工单并发冲突
	ErrTaskConflict = "M5-E-2008"

	// 2901~2902 查询
	// ErrTaskListFailed 调度工单列表查询失败
	ErrTaskListFailed = "M5-E-2901"
	// ErrTaskQueryFailed 调度工单查询失败
	ErrTaskQueryFailed = "M5-E-2902"

	// W 级: 参数校验(HTTP 400)
	// ErrDispatchParamInvalid 调度参数非法
	ErrDispatchParamInvalid = "M5-W-2001"
)
