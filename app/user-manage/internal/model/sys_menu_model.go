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
