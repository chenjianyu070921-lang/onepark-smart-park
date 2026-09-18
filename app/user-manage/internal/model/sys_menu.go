package model

import "time"

// SysMenu 系统菜单/权限表.
type SysMenu struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ParentID   uint64    `gorm:"column:parent_id;type:bigint;not null;default:0;index:idx_parent" json:"parent_id"`
	MenuKey    string    `gorm:"column:menu_key;type:varchar(64);uniqueIndex:uk_menu_key;not null" json:"menu_key"`
	MenuName   string    `gorm:"column:menu_name;type:varchar(64);not null;default:''" json:"menu_name"`
	Permission string    `gorm:"column:permission;type:varchar(128);not null;default:'';index:idx_permission" json:"permission"`
	Path       string    `gorm:"column:path;type:varchar(255);not null;default:''" json:"path"`
	Sort       int       `gorm:"column:sort;type:int;not null;default:0" json:"sort"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (SysMenu) TableName() string {
	return "sys_menu"
}
