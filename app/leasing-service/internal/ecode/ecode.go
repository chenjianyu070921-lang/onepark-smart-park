// Package ecode 定义 M5 运营招商模块的错误码
// 格式: {Module}-{Level}-{Code}, 见 common/errorx/codes.go
package ecode

const (
	// 1001~1007 合同/账单业务
	// ErrContractNotFound 合同不存在
	ErrContractNotFound = "M5-E-1001"
	// ErrContractStatusInvalid 合同状态不允许该操作
	ErrContractStatusInvalid = "M5-E-1002"
	// ErrContractCreateFailed 合同创建失败
	ErrContractCreateFailed = "M5-E-1003"
	// ErrContractUpdateFailed 合同更新失败
	ErrContractUpdateFailed = "M5-E-1004"
	// ErrBillGenerateFailed 账单生成失败
	ErrBillGenerateFailed = "M5-E-1005"
	// ErrZoneCodeInvalid 区域编码非法
	ErrZoneCodeInvalid = "M5-E-1006"
	// ErrContractConflict 合同已被并发修改
	ErrContractConflict = "M5-E-1007"

	// 1901~1904 统计/查询
	// ErrContractListFailed 合同列表查询失败
	ErrContractListFailed = "M5-E-1901"
	// ErrContractQueryFailed 合同查询失败
	ErrContractQueryFailed = "M5-E-1902"
	// ErrOccupancyStatFailed 入驻率统计失败
	ErrOccupancyStatFailed = "M5-E-1903"
	// ErrExpiringQueryFailed 到期合同查询失败
	ErrExpiringQueryFailed = "M5-E-1904"

	// W 级: 参数校验(HTTP 400)
	// ErrLeaseParamInvalid 招商参数非法
	ErrLeaseParamInvalid = "M5-W-1001"
)
