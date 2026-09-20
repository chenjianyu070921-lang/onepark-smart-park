// Package ecode 定义 M4 能源管控模块的错误码
// 格式: {Module}-{Level}-{Code}, 见 common/errorx/codes.go
package ecode

const (
	// ErrDeviceNoData 该设备还没有任何能耗数据
	ErrDeviceNoData = "M4-E-1001"
	// ErrQueryFailed 查询能耗数据失败(数据库异常)
	ErrQueryFailed = "M4-E-1002"
	// ErrBadTimeRange 时间范围参数不合法
	ErrBadTimeRange = "M4-E-1003"
	// ErrZoneNoData 该区域在指定时间范围内没有任何数据
	ErrZoneNoData = "M4-E-1004"
)
