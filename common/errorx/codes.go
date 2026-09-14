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

// M1 物联接入底座错误码
const (
	ErrProductNotFound   = "M1-E-1001" // 产品不存在
	ErrDeviceDuplicate    = "M1-E-1002" // 设备名重复
	ErrDeviceCreateFail   = "M1-E-1003" // 设备写入失败
	ErrShadowCreateFail   = "M1-E-1004" // 影子创建失败
	ErrDeviceNotFound     = "M1-E-1005" // 设备不存在
	ErrDeviceOffline      = "M1-E-1006" // 设备离线
	ErrCommandSendFail    = "M1-E-1007" // 指令下发失败
)
