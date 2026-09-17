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
	ErrBadRequest   = "M6-E-0001" // 请求参数错误
	ErrUnauthorized = "M6-E-0002" // 未登录/Token 失效
	ErrForbidden    = "M6-E-0003" // 无权限
	ErrNotFound     = "M6-E-0004" // 资源不存在
	ErrInternal     = "M6-E-0005" // 服务器内部错误
	ErrDepConnect   = "M6-E-0006" // 依赖中间件连接失败
	ErrRateLimited  = "M6-E-0007" // 请求过于频繁(限流)
	ErrBadGateway   = "M6-E-0008" // 网关上游服务不可用
)

// M3 园区安防错误码: 1xxx 告警 / 2xxx 门禁 / 3xxx 视频.
// 注意: 参数校验类务必用 M3-W-xxxx 才返回 400 (KI-2).
const (
	// 告警 (alarm-service)
	ErrAlarmRuleCreate    = "M3-E-1001" // 告警规则创建失败
	ErrAlarmRuleNotFound  = "M3-E-1002" // 告警规则不存在
	ErrAlarmNotFound      = "M3-E-1003" // 告警记录不存在
	ErrAlarmAck           = "M3-E-1004" // 告警确认失败
	ErrAlarmResolve       = "M3-E-1005" // 告警解决失败
	ErrAlarmQuery         = "M3-E-1006" // 告警查询失败
	ErrAlarmStatusInvalid = "M3-E-1007" // 告警状态不允许该操作
	// ErrAlarmNotify 告警事件发送 M5 失败(docs/m3/04 #40): 告警状态已落库, 需人工/任务补偿.
	// 占用 1008 的原因: docs 原把 1007 留给本场景, 但 1007 已在实现中用于"状态不允许";
	// 而 docs 的 1008(ES 查询失败)已随"ES 故障降级 MySQL"落地——降级不再对调用方报错, 该码位空出.
	ErrAlarmNotify = "M3-E-1008"
	// 参数校验类错误必须用 W 级别才能返回 400, 见 KI-2 与 docs/m3/04 §6 注.
	ErrAlarmParamInvalid = "M3-W-1001" // 告警参数非法(HTTP 400)

	// 门禁 (access-control-service)
	ErrAccessGrant      = "M3-E-2001" // 门禁授权失败
	ErrAccessRevoke     = "M3-E-2002" // 门禁撤销失败
	ErrAccessRemoteOpen = "M3-E-2003" // 远程开门失败
	ErrAccessRecord     = "M3-E-2004" // 通行记录查询失败
	// 参数校验类错误必须用 W 级别才能返回 400, 见 KI-2 与 docs/m3/04 §6 注.
	ErrAccessParamInvalid = "M3-W-2001" // 门禁参数非法(HTTP 400)

	// 视频 (video-service)
	ErrVideoCameraCreate   = "M3-E-3001" // 摄像头添加失败
	ErrVideoCameraNotFound = "M3-E-3002" // 摄像头不存在
	ErrVideoStream         = "M3-E-3003" // 视频流地址获取失败
	ErrVideoStreamNotFound = "M3-E-3004" // 取流: 摄像头不存在
	ErrVideoCameraOffline  = "M3-E-3005" // 取流: 设备离线
	// 参数校验类错误必须用 W 级别才能返回 400, 见 KI-2 与 docs/m3/04 §6 注.
	ErrVideoParamInvalid = "M3-W-3001" // 视频参数非法(HTTP 400), 如 RTSP 地址格式错误
)

// M1 物联接入底座错误码
const (
	ErrProductNotFound  = "M1-E-1001" // 产品不存在
	ErrDeviceDuplicate  = "M1-E-1002" // 设备名重复
	ErrDeviceCreateFail = "M1-E-1003" // 设备写入失败
	ErrShadowCreateFail = "M1-E-1004" // 影子创建失败
	ErrDeviceNotFound   = "M1-E-1005" // 设备不存在
	ErrDeviceOffline    = "M1-E-1006" // 设备离线
	ErrCommandSendFail  = "M1-E-1007" // 指令下发失败
	ErrShadowNotFound   = "M1-E-1008" // 设备影子不存在
	ErrShadowVersionDup = "M1-E-1009" // 影子版本冲突(乐观锁)

	// W 级: 参数校验类, HttpStatus 映射为 400
	ErrDeviceParamInvalid = "M1-W-1001" // 设备请求参数非法
)

// M2 物业管理服务业务错误码
const (
	// 工单域 1001~1999
	ErrWorkOrderNotFound       = "M2-E-1001" // 工单不存在
	ErrWorkOrderStatusInvalid  = "M2-E-1002" // 工单状态非法或流转被禁止
	ErrWorkOrderAssignConflict = "M2-E-1003" // 派单并发冲突(乐观锁 version 不一致)
	ErrWorkOrderStatusConflict = "M2-E-1004" // 工单状态流转并发冲突(乐观锁 version 不一致)

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

	// 用户管理域 6001~6099
	ErrUserNotFound     = "M2-E-6001" // 用户不存在
	ErrUserDuplicate    = "M2-E-6002" // 用户名重复
	ErrRoleNotFound     = "M2-E-6003" // 角色不存在
	ErrRoleDuplicate    = "M2-E-6004" // 角色标识重复
	ErrMenuNotFound     = "M2-E-6005" // 菜单不存在
	ErrPermissionDenied = "M2-E-6006" // 无权限(角色未分配该菜单权限)
	ErrMenuDuplicate    = "M2-E-6007" // 菜单标识重复
)

// M4 能源管控错误码: 1xxx 采集与能耗查询(energy-data/energy-analysis 共用) / 2xxx 计费(billing).
// 与各服务 internal/ecode 保持同步(来源: billing/energy-data/energy-analysis 三处 ecode.go).
const (
	// 采集与分析 (energy-data / energy-analysis)
	ErrDeviceNoData = "M4-E-1001" // 该设备还没有任何能耗数据
	ErrQueryFailed  = "M4-E-1002" // 查询能耗数据失败(数据库异常)
	ErrBadTimeRange = "M4-E-1003" // 时间范围参数不合法
	ErrZoneNoData   = "M4-E-1004" // 该区域在指定时间范围内没有数据

	// 计费 (billing)
	ErrBadRuleConfig = "M4-E-1005" // 规则配置不合法(阶梯档位未递增/峰谷时间写错)
	ErrRuleNotFound  = "M4-E-1006" // 规则不存在
	ErrNoRuleMatch   = "M4-E-1007" // 该区域既无专属规则也无全局默认规则
	ErrBillExists    = "M4-E-1008" // 该账期已经出过账, 不能重复出
	ErrNoUsage       = "M4-E-1009" // 该区域这个账期没有任何用量, 出不了账
)

// M5 运营招商+指挥调度+大屏错误码: 招商 leasing 1001~1999 / 调度 dispatch 2001~2999 / 大屏 dashboard 3001~3999.
// 与 M5 各服务 internal/ecode 保持同步(来源: leasing/dispatch 两处 ecode.go; dashboard 暂沿用中央码, 待其接入 errorx).
// 参数校验类统一用 W 级(M5-W-xxxx)以保证返回 400(见 http_status.go 与 M3 KI-2).
const (
	// 招商 leasing (1001~1999)
	ErrContractNotFound      = "M5-E-1001" // 合同不存在
	ErrContractStatusInvalid = "M5-E-1002" // 合同状态不允许该操作
	ErrContractCreateFailed  = "M5-E-1003" // 合同创建失败
	ErrContractUpdateFailed  = "M5-E-1004" // 合同更新失败
	ErrBillGenerateFailed    = "M5-E-1005" // 账单生成失败
	ErrZoneCodeInvalid       = "M5-E-1006" // 区域编码非法
	ErrContractConflict      = "M5-E-1007" // 合同已被并发修改
	ErrContractListFailed    = "M5-E-1901" // 合同列表查询失败
	ErrContractQueryFailed   = "M5-E-1902" // 合同查询失败
	ErrOccupancyStatFailed   = "M5-E-1903" // 入驻率统计失败
	ErrExpiringQueryFailed   = "M5-E-1904" // 到期合同查询失败
	// W 级: 参数校验(HTTP 400)
	ErrLeaseParamInvalid = "M5-W-1001" // 招商参数非法(HTTP 400)

	// 调度 dispatch (2001~2999)
	ErrTaskNotFound         = "M5-E-2001" // 调度工单不存在
	ErrTaskCreateFailed     = "M5-E-2002" // 调度工单创建失败
	ErrTaskUpdateFailed     = "M5-E-2003" // 调度工单更新失败
	ErrTaskStatusInvalid    = "M5-E-2004" // 调度工单状态不允许该操作
	ErrAssigneeLoadFailed   = "M5-E-2005" // 候选处理人加载失败
	ErrAssignFailed         = "M5-E-2006" // 指派失败
	ErrNoAssignee           = "M5-E-2007" // 暂无可用处理人
	ErrTaskConflict         = "M5-E-2008" // 调度工单并发冲突
	ErrTaskListFailed       = "M5-E-2901" // 调度工单列表查询失败
	ErrTaskQueryFailed      = "M5-E-2902" // 调度工单查询失败
	// W 级: 参数校验(HTTP 400)
	ErrDispatchParamInvalid = "M5-W-2001" // 调度参数非法(HTTP 400)

	// 大屏 dashboard (3001~3999)
	ErrDashboardStatFailed   = "M5-E-3001" // 大屏统计失败
	ErrDashboardParamInvalid = "M5-W-3001" // 大屏参数非法(HTTP 400)
)
