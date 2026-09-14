package errorx

// 错误码格式: {Module}-{Level}-{Code}
// Module: M1~M6
// Level:  E(Error) W(Warn) I(Info)
// Code:   4位数字, 0001~9999
//
// 各服务按自身模块前缀定义错误码, 不允许跨模块占用前缀.
// 示例: M1-E-1001 设备注册失败, M6-E-1001 鉴权失败

// 模块前缀
const (
	ModuleM1 = "M1" // 物联接入底座
	ModuleM2 = "M2" // 物业管理
	ModuleM3 = "M3" // 园区安防
	ModuleM4 = "M4" // 能源管控
	ModuleM5 = "M5" // 运营招商+指挥调度
	ModuleM6 = "M6" // 公共基础服务+DevOps
)

// 级别
const (
	LevelError = "E"
	LevelWarn  = "W"
	LevelInfo  = "I"
)

// 通用成功码
const SuccessCode = "0"

// 通用错误码 (M6 前缀, 公共部分)
const (
	ErrBadRequest    = "M6-E-0001" // 请求参数错误
	ErrUnauthorized  = "M6-E-0002" // 未登录/Token 失效
	ErrForbidden     = "M6-E-0003" // 无权限
	ErrNotFound      = "M6-E-0004" // 资源不存在
	ErrInternal      = "M6-E-0005" // 服务器内部错误
	ErrDepConnect    = "M6-E-0006" // 依赖中间件连接失败
)

// M2 物业管理服务业务错误码
const (
	// 工单域 1001~1999
	ErrWorkOrderNotFound      = "M2-E-1001" // 工单不存在
	ErrWorkOrderStatusInvalid = "M2-E-1002" // 工单状态非法或流转被禁止
	ErrWorkOrderAssignFailed  = "M2-E-1003" // 工单派单失败（并发冲突或处理人不合法）

	// 访客域 2001~2999
	ErrVisitorQRCodeExpired = "M2-E-2001" // 访客二维码已过期
	ErrVisitorQRCodeUsed    = "M2-E-2002" // 访客二维码已被核销
	ErrVisitorCheckinFailed = "M2-E-2003" // 访客签入失败（gRPC 开门失败等）

	// 停车域 3001~3999
	ErrParkingRecordNotFound = "M2-E-3001" // 停车记录不存在
	ErrParkingCalcFeeFailed  = "M2-E-3002" // 停车计费失败
	ErrParkingDeviceNotFound = "M2-E-3003" // 停车设备不存在或未接入

	// 公告域 4001~4999
	ErrNoticeNotFound = "M2-E-4001" // 公告不存在

	// 公共域 5001~5999
	ErrM2Internal = "M2-E-5001" // M2 服务内部错误兜底
)
