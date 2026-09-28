package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	// SysMenuModel 菜单/权限 CRUD.
	SysMenuModel interface {
		Insert(ctx context.Context, m *SysMenu) error
		FindByID(ctx context.Context, id uint64) (*SysMenu, error)
		FindByKey(ctx context.Context, menuKey string) (*SysMenu, error)
		FindList(ctx context.Context) ([]*SysMenu, error)
		// FindListByRoleID 按角色返回已授权菜单(JOIN sys_role_menu), 供前端菜单树按角色渲染.
		FindListByRoleID(ctx context.Context, roleID uint64) ([]*SysMenu, error)
	}

	sysMenuModel struct {
		db *gorm.DB
	}
)

func NewSysMenuModel(db *gorm.DB) SysMenuModel {
	return &sysMenuModel{db: db}
}

func (m *sysMenuModel) Insert(ctx context.Context, menu *SysMenu) error {
	return m.db.WithContext(ctx).Create(menu).Error
}

func (m *sysMenuModel) FindByID(ctx context.Context, id uint64) (*SysMenu, error) {
	var menu SysMenu
	if err := m.db.WithContext(ctx).Where("id = ?", id).First(&menu).Error; err != nil {
		return nil, err
	}
	return &menu, nil
}

func (m *sysMenuModel) FindByKey(ctx context.Context, menuKey string) (*SysMenu, error) {
	var menu SysMenu
	if err := m.db.WithContext(ctx).Where("menu_key = ?", menuKey).First(&menu).Error; err != nil {
		return nil, err
	}
	return &menu, nil
}

func (m *sysMenuModel) FindList(ctx context.Context) ([]*SysMenu, error) {
	var list []*SysMenu
	if err := m.db.WithContext(ctx).Order("sort ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (m *sysMenuModel) FindListByRoleID(ctx context.Context, roleID uint64) ([]*SysMenu, error) {
	var list []*SysMenu
	// 只取该角色被授权的菜单; 目录节点如果自身未授权, 其子节点也不会出现,
	// 前端过滤时需保留"子节点可见"的父节点(见 buildVisibleMenu).
	err := m.db.WithContext(ctx).Model(&SysMenu{}).
		Joins("JOIN sys_role_menu rm ON rm.menu_id = sys_menu.id").
		Where("rm.role_id = ?", roleID).
		Order("sys_menu.sort ASC, sys_menu.id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
