package model

import (
	"context"

	"gorm.io/gorm"
)

// SysRoleMenu 角色-菜单(权限)关联(多对多).
type SysRoleMenu struct {
	ID     uint64 `gorm:"primaryKey;autoIncrement"`
	RoleID uint64 `gorm:"column:role_id;type:bigint;not null;uniqueIndex:uk_role_menu,priority:1"`
	MenuID uint64 `gorm:"column:menu_id;type:bigint;not null;uniqueIndex:uk_role_menu,priority:2"`
}

func (SysRoleMenu) TableName() string {
	return "sys_role_menu"
}

type (
	SysRoleMenuModel interface {
		Assign(ctx context.Context, roleID, menuID uint64) error
		ListMenuIDs(ctx context.Context, roleID uint64) ([]uint64, error)
		// ListPermissions 汇总多角色去重后的 permission 列表(通过 role_menu JOIN menu).
		ListPermissions(ctx context.Context, roleIDs []uint64) ([]string, error)
	}

	sysRoleMenuModel struct {
		db *gorm.DB
	}
)

func NewSysRoleMenuModel(db *gorm.DB) SysRoleMenuModel {
	return &sysRoleMenuModel{db: db}
}

func (m *sysRoleMenuModel) Assign(ctx context.Context, roleID, menuID uint64) error {
	rm := &SysRoleMenu{RoleID: roleID, MenuID: menuID}
	return m.db.WithContext(ctx).Create(rm).Error
}

func (m *sysRoleMenuModel) ListMenuIDs(ctx context.Context, roleID uint64) ([]uint64, error) {
	var ids []uint64
	if err := m.db.WithContext(ctx).Model(&SysRoleMenu{}).
		Where("role_id = ?", roleID).Pluck("menu_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (m *sysRoleMenuModel) ListPermissions(ctx context.Context, roleIDs []uint64) ([]string, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	var perms []string
	err := m.db.WithContext(ctx).
		Model(&SysRoleMenu{}).
		Distinct("m.permission").
		Joins("JOIN sys_menu m ON m.id = sys_role_menu.menu_id").
		Where("sys_role_menu.role_id IN ? AND m.permission <> ''", roleIDs).
		Pluck("m.permission", &perms).Error
	if err != nil {
		return nil, err
	}
	return perms, nil
}
