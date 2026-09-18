package model

import "time"

// SysRole 系统角色表.
type SysRole struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RoleKey   string    `gorm:"column:role_key;type:varchar(64);uniqueIndex:uk_role_key;not null" json:"role_key"`
	RoleName  string    `gorm:"column:role_name;type:varchar(64);not null;default:''" json:"role_name"`
	Remark    string    `gorm:"column:remark;type:varchar(255);not null;default:''" json:"remark"`
	DataScope int8      `gorm:"column:data_scope;type:tinyint;not null;default:2" json:"data_scope"` // 数据权限范围: 1全部 2本园区/租户 4本人
	CreatedAt time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (SysRole) TableName() string {
	return "sys_role"
}
