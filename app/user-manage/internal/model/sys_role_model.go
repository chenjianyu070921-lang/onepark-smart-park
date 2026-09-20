package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	// SysRoleModel 角色 CRUD.
	SysRoleModel interface {
		Insert(ctx context.Context, r *SysRole) error
		FindByID(ctx context.Context, id uint64) (*SysRole, error)
		FindByKey(ctx context.Context, roleKey string) (*SysRole, error)
		FindList(ctx context.Context) ([]*SysRole, error)
	}

	sysRoleModel struct {
		db *gorm.DB
	}
)

func NewSysRoleModel(db *gorm.DB) SysRoleModel {
	return &sysRoleModel{db: db}
}

func (m *sysRoleModel) Insert(ctx context.Context, r *SysRole) error {
	return m.db.WithContext(ctx).Create(r).Error
}

func (m *sysRoleModel) FindByID(ctx context.Context, id uint64) (*SysRole, error) {
	var r SysRole
	if err := m.db.WithContext(ctx).Where("id = ?", id).First(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

func (m *sysRoleModel) FindByKey(ctx context.Context, roleKey string) (*SysRole, error) {
	var r SysRole
	if err := m.db.WithContext(ctx).Where("role_key = ?", roleKey).First(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

func (m *sysRoleModel) FindList(ctx context.Context) ([]*SysRole, error) {
	var list []*SysRole
	if err := m.db.WithContext(ctx).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
