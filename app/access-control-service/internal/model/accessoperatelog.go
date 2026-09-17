// Package model 定义 access-control-service 的 GORM 数据模型.
// 对齐 docs/m3/04-接口与数据契约.md §2.2 的 access_db 表结构.
// 约定: 每服务独立库(access_db), 禁止跨库 JOIN; 表均携带 tenant_id 作 RBAC 行级隔离.
package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// 开门命令结果取值, 与 access_operate_log.result 及 docs/m3/04 §2.2 一致.
const (
	CommandResultFail    int8 = 0 // 失败
	CommandResultSuccess int8 = 1 // 成功
	CommandResultTimeout int8 = 2 // 超时
)

// BaseModel 业务表公共基础字段: 物理主键 + 租户隔离 + 时间戳.
type BaseModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID  int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// AccessOperateLog 开门操作审计表(access_db.access_operate_log).
// 远程开门的成功/失败/超时三种结果都需留痕(docs/m3/04 #47).
type AccessOperateLog struct {
	BaseModel
	DeviceID   string `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	OperatorID int64  `gorm:"column:operator_id;not null;default:0" json:"operator_id"`
	Command    string `gorm:"column:command;type:varchar(32);not null;default:''" json:"command"`
	Result     int8   `gorm:"column:result;not null;default:0" json:"result"`
	Message    string `gorm:"column:message;type:varchar(255);not null;default:''" json:"message"`
	Reason     string `gorm:"column:reason;type:varchar(255);not null;default:''" json:"reason"`
}

// TableName 指定开门操作审计表名.
func (AccessOperateLog) TableName() string { return "access_operate_log" }

// OperateLogModel 审计数据访问层, 接口化以便单测替换(见 internal/logic 单测).
type OperateLogModel interface {
	// Create 写入一条开门操作审计.
	Create(ctx context.Context, log *AccessOperateLog) error
}

type operateLogModel struct {
	db *gorm.DB
}

// NewOperateLogModel 构造基于 GORM 的审计数据访问实现.
func NewOperateLogModel(db *gorm.DB) OperateLogModel {
	return &operateLogModel{db: db}
}

func (m *operateLogModel) Create(ctx context.Context, log *AccessOperateLog) error {
	return m.db.WithContext(ctx).Create(log).Error
}
