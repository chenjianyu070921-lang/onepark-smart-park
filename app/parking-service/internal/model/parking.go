// Package model 定义 parking-service 的 GORM 数据模型(对齐 docs/M2设计文档V1.1 第七章停车域).
// 约定: 每服务独立库(parking_db), 禁止跨库 JOIN; 同库内可用 GORM 关联.
// 所有业务表统一携带 tenant_id(RBAC 数据隔离) 与时间戳.
package model

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"onepark/common/gormx"
)

// ErrDuplicateRequest 幂等键冲突(uk_request 命中), 表示该遥测消息此前已建过停车记录.
var ErrDuplicateRequest = errors.New("parking: duplicate request")

// BaseModel 业务表公共基础字段: 物理主键 + 租户隔离 + 时间戳.
// TenantID 由网关注入的 x-tenant-id 写入, 作为 RBAC 行级隔离维度.
type BaseModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID  int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"` // 园区ID, RBAC 数据隔离维度
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// ParkingRecord 停车记录, 入场/离场由 M1 Kafka 地磁事件驱动(亦提供 HTTP 入口便于联调).
// fee 在库中为 DECIMAL, 模型以 float64 承载, 接口层再格式化为字符串避免浮点误差.
type ParkingRecord struct {
	BaseModel
	PlateNo     string     `gorm:"column:plate_no;type:varchar(32);not null" json:"plate_no"`             // 车牌号
	EntryTime   *time.Time `gorm:"column:entry_time" json:"entry_time"`                                   // 入场时间
	ExitTime    *time.Time `gorm:"column:exit_time" json:"exit_time"`                                     // 离场时间
	DurationMin *int       `gorm:"column:duration_min" json:"duration_min"`                               // 停车时长(分钟)
	Fee         float64    `gorm:"column:fee;type:decimal(10,2)" json:"fee"`                              // 停车费
	VehicleType int8       `gorm:"column:vehicle_type;not null;default:1" json:"vehicle_type"`            // 1月卡 2临时 3VIP 4异常
	Status      int8       `gorm:"column:status;not null;default:1" json:"status"`                        // 1停车中 2已完成
	DeviceIDIn  string     `gorm:"column:device_id_in;type:varchar(64);default:''" json:"device_id_in"`   // 入场地磁设备ID
	DeviceIDOut string     `gorm:"column:device_id_out;type:varchar(64);default:''" json:"device_id_out"` // 出场地磁设备ID
	// RequestID 消费幂等键(L3 兜底): 同一条地磁遥测重复投递时靠唯一索引拦截, 避免重复建单/重复计费.
	// 用指针且不加 not null: HTTP 联调入口不携带幂等键时写入 NULL,
	// MySQL 唯一索引允许多个 NULL, 空串则会互相撞键(多行 '' 直接 1062).
	RequestID *string `gorm:"column:request_id;type:varchar(64);uniqueIndex:uk_request" json:"request_id,omitempty"`
}

// TableName 指定停车记录表名(对齐 parking_db 库).
func (ParkingRecord) TableName() string { return "parking_record" }

// ParkingFeeRule 停车计费规则, 以 JSON 存储阶梯/封顶等配置.
type ParkingFeeRule struct {
	BaseModel
	RuleJSON      string     `gorm:"column:rule_json;type:json" json:"rule_json"` // 计费规则(JSON)
	EffectiveFrom *time.Time `gorm:"column:effective_from" json:"effective_from"`
	EffectiveTo   *time.Time `gorm:"column:effective_to" json:"effective_to"`
}

// TableName 指定停车计费规则表名.
func (ParkingFeeRule) TableName() string { return "parking_fee_rule" }

// ParkingFeeRuleConfig 计费规则反序列化结构(对应 parking_fee_rule.rule_json).
// 字段: 免费时长(分钟)/每小时单价(元)/每日封顶(元, 0=不封顶).
// 以 JSON 存储便于后续扩展阶梯/分车型费率而不改表结构.
type ParkingFeeRuleConfig struct {
	FreeMinutes int     `json:"free_minutes"` // 免费时长(分钟)
	HourlyFee   float64 `json:"hourly_fee"`   // 每小时单价(元)
	DailyCap    float64 `json:"daily_cap"`    // 每日封顶(元, 0=不封顶)
}

// ParseRule 解析计费规则 JSON; 解析失败返回错误(调用方降级为内置默认规则).
func (r *ParkingFeeRule) ParseRule() (*ParkingFeeRuleConfig, error) {
	if r == nil || r.RuleJSON == "" {
		return nil, errors.New("parking: empty fee rule")
	}
	var cfg ParkingFeeRuleConfig
	if err := json.Unmarshal([]byte(r.RuleJSON), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetActiveParkingFeeRule 取当前生效的计费规则: 生效时间窗覆盖 at 的最新一条.
// db 为 nil 时返回 (nil, nil) 由调用方降级为内置默认规则; 查不到返回 (nil, nil).
func GetActiveParkingFeeRule(db *gormx.DB, tenantID int64, at time.Time) (*ParkingFeeRule, error) {
	if db == nil {
		return nil, nil
	}
	var rule ParkingFeeRule
	q := db.Model(&ParkingFeeRule{}).Where("tenant_id = ?", tenantID)
	q = q.Where("(effective_from IS NULL OR effective_from <= ?)", at)
	q = q.Where("(effective_to IS NULL OR effective_to >= ?)", at)
	if err := q.Order("id DESC").First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rule, nil
}

// 停车状态码(与 ParkingRecord.Status 字段含义一致)
const (
	ParkingStatusParking int8 = 1 // 停车中
	ParkingStatusDone    int8 = 2 // 已完成(已离场计费)
)

// 车辆类型(与 ParkingRecord.VehicleType 字段含义一致)
const (
	VehicleTypeMonthly  int8 = 1 // 月卡
	VehicleTypeTemp     int8 = 2 // 临时
	VehicleTypeVIP      int8 = 3 // VIP
	VehicleTypeAbnormal int8 = 4 // 异常
)
