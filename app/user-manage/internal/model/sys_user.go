package model

import (
	"time"

	"gorm.io/gorm"
)

// SysUser 系统用户表.
type SysUser struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	Username  string         `gorm:"column:username;type:varchar(64);uniqueIndex:uk_username;not null" json:"username"`
	Password  string         `gorm:"column:password;type:varchar(255);not null" json:"-"`
	Nickname  string         `gorm:"column:nickname;type:varchar(64);not null;default:''" json:"nickname"`
	Status    int8           `gorm:"column:status;type:tinyint;not null;default:1" json:"status"`
	// TenantId 所属园区/租户ID, RBAC 数据隔离维度(用户归属园区). 由网关注入的 x-tenant-id 经 datascope 隔离.
	TenantId  int64          `gorm:"column:tenant_id;type:bigint;not null;default:1" json:"tenant_id"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index:idx_deleted_at" json:"deleted_at"`
}

func (SysUser) TableName() string {
	return "sys_user"
}
