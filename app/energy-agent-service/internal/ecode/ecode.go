// Package ecode 定义 M4 能源管控模块的错误码
// 格式: {Module}-{Level}-{Code}, 见 common/errorx/codes.go
package ecode

const (
	// 1001~1004 能源采集与分析(energy-data / energy-analysis 共用)
	// ErrDeviceNoData 该设备还没有任何能耗数据
	ErrDeviceNoData = "M4-E-1001"
	// ErrQueryFailed 查询能耗数据失败(数据库异常)
	ErrQueryFailed = "M4-E-1002"
	// ErrBadTimeRange 时间范围参数不合法
	ErrBadTimeRange = "M4-E-1003"
	// ErrZoneNoData 该区域在指定时间范围内没有数据
	ErrZoneNoData = "M4-E-1004"

	// 1005~1009 计费(billing)
	// ErrBadRuleConfig 规则配置不合法, 比如阶梯档位没递增、峰谷时间写错
	ErrBadRuleConfig = "M4-E-1005"
	// ErrRuleNotFound 规则不存在
	ErrRuleNotFound = "M4-E-1006"
	// ErrNoRuleMatch 这个区域既没有专属规则, 也没有全园区默认规则
	ErrNoRuleMatch = "M4-E-1007"
	// ErrBillExists 该账期已经出过账了, 不能重复出
	ErrBillExists = "M4-E-1008"
	// ErrNoUsage 该区域这个账期没有任何用量, 出不了账
	ErrNoUsage = "M4-E-1009"

	// 1010~1014 能源归因智能体(energy-agent)
	// ErrNoEnergyData 库里没有任何能耗数据, 巡检无从下手
	ErrNoEnergyData = "M4-E-1010"
	// ErrSuggestionNotFound 建议不存在(可能已被清理)
	ErrSuggestionNotFound = "M4-E-1011"
	// ErrSuggestionReviewed 该建议已经审批过了, 不能重复审批
	ErrSuggestionReviewed = "M4-E-1012"
	// ErrInspectFailed 巡检执行失败
	ErrInspectFailed = "M4-E-1013"
)
