// Package model 定义 alarm-service 的 GORM 数据模型.
// 对齐 docs/m3/04-接口与数据契约.md §2.1 的 alarm_db 表结构.
// 约定: 每服务独立库(alarm_db), 禁止跨库 JOIN; 表均携带 tenant_id 作 RBAC 行级隔离.
package model

import (
	"errors"
	"time"
)

// ErrDuplicateRequest 请求幂等键冲突(L3 唯一索引命中), 表示该消息此前已生成过告警.
var ErrDuplicateRequest = errors.New("alarm: duplicate request")

// BaseModel 业务表公共基础字段: 物理主键 + 租户隔离 + 时间戳.
type BaseModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID  int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// Alarm 告警记录表(alarm_db.alarm).
type Alarm struct {
	BaseModel
	AlarmNo string `gorm:"column:alarm_no;type:varchar(32);not null;uniqueIndex" json:"alarm_no"`
	// RuleID 与 RequestID 组成复合唯一索引 uk_request_rule: 同一事件命中多条规则时各生成一条告警,
	// 互不被去重; 同一事件 + 同一规则重复上报才被拦截(L3).
	RuleID    int64  `gorm:"column:rule_id;not null;default:0;uniqueIndex:uk_request_rule,priority:1" json:"rule_id"`
	DeviceID  string `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	AreaID    int64  `gorm:"column:area_id;not null;default:0" json:"area_id"`
	EventType string `gorm:"column:event_type;type:varchar(32);not null;default:''" json:"event_type"`
	// Level/Status 不设 default 标签: GORM 创建时会跳过带 default 的零值字段,
	// 若依赖列默认值, status=0(未处理)/level=0 都可能在换库改默认值时静默写错.
	// 列默认值仅作为直接 SQL 插入的兜底, 应用层必须显式提供这两个字段.
	Level     int8       `gorm:"column:level;not null" json:"level"`
	Status    int8       `gorm:"column:status;not null" json:"status"`
	Content   string     `gorm:"column:content;type:varchar(512);not null;default:''" json:"content"`
	RequestID string     `gorm:"column:request_id;type:varchar(64);not null;uniqueIndex:uk_request_rule,priority:2" json:"request_id"`
	AckBy     int64      `gorm:"column:ack_by;not null;default:0" json:"ack_by"`
	AckAt     *time.Time `gorm:"column:ack_at" json:"ack_at"`
	ResolveBy int64      `gorm:"column:resolve_by;not null;default:0" json:"resolve_by"`
	ResolveAt *time.Time `gorm:"column:resolve_at" json:"resolve_at"`
}

// TableName 指定告警记录表名.
func (Alarm) TableName() string { return "alarm" }

// AlarmRule 告警规则表(alarm_db.alarm_rule), 由规则引擎 Evaluate 读取.
//
// 范围匹配维度: DeviceType / DeviceID / AreaID / EventType, 空值表示该维度不限;
// 命中后按 Level 生成告警等级 —— 这就是「按设备类型 + 事件类型映射告警等级」的配置面.
type AlarmRule struct {
	BaseModel
	Name string `gorm:"column:name;type:varchar(64);not null;default:''" json:"name"`
	// DeviceType 设备类型(access_control/camera/sensor...); 空表示不限设备类型.
	DeviceType    string `gorm:"column:device_type;type:varchar(32);not null;default:''" json:"device_type"`
	DeviceID      string `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	AreaID        int64  `gorm:"column:area_id;not null;default:0" json:"area_id"`
	EventType     string `gorm:"column:event_type;type:varchar(32);not null;default:''" json:"event_type"`
	RuleType      string `gorm:"column:rule_type;type:varchar(16);not null;default:threshold" json:"rule_type"`
	Conditions    string `gorm:"column:conditions;type:json" json:"conditions"`
	WindowSeconds int    `gorm:"column:window_seconds;not null;default:0" json:"window_seconds"`
	// Level/Status 同样不可加 default 标签: 否则 status=0(禁用)会被 GORM 当作零值跳过,
	// 取列默认值 1(启用) —— 规则禁用功能会静默失效(与 access_record.result 同一陷阱).
	Level  int8 `gorm:"column:level;not null" json:"level"`
	Status int8 `gorm:"column:status;not null" json:"status"`
}

// TableName 指定告警规则表名.
func (AlarmRule) TableName() string { return "alarm_rule" }

// AlarmOperateLog 告警处理流水(alarm_db.alarm_operate_log), 记录每条告警的状态变更审计.
type AlarmOperateLog struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID   int64     `gorm:"column:tenant_id;not null;default:0" json:"tenant_id"`
	AlarmID    int64     `gorm:"column:alarm_id;not null;default:0;index:idx_alarm" json:"alarm_id"`
	Action     string    `gorm:"column:action;type:varchar(16);not null;default:''" json:"action"`
	OperatorID int64     `gorm:"column:operator_id;not null;default:0" json:"operator_id"`
	Remark     string    `gorm:"column:remark;type:varchar(255);not null;default:''" json:"remark"`
	CreatedAt  time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

// TableName 指定告警处理流水表名.
func (AlarmOperateLog) TableName() string { return "alarm_operate_log" }

// 告警等级(1-4), 与 docs/m3/04 §5.1 一致.
const (
	AlarmLevelInfo     int8 = 1 // 提示: 一般提示性事件
	AlarmLevelMinor    int8 = 2 // 一般: 需关注
	AlarmLevelMajor    int8 = 3 // 严重: 需及时处理
	AlarmLevelCritical int8 = 4 // 紧急: 需立即响应, M5 优先派单
)

// 告警状态(0/1/2), 与 docs/m3/04 §5.2 一致. 活跃告警 = AlarmStatusPending.
const (
	AlarmStatusPending  int8 = 0 // 未处理(初始态, GetActiveAlarms 聚合此态)
	AlarmStatusAcked    int8 = 1 // 已确认
	AlarmStatusResolved int8 = 2 // 已解决(终态)
)

// 告警操作动作, 写入 alarm_operate_log.action.
const (
	AlarmActionCreate  = "create"
	AlarmActionAck     = "ack"
	AlarmActionResolve = "resolve"
)
