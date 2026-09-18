package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	// SysUserModel 用户 CRUD.
	SysUserModel interface {
		Insert(ctx context.Context, u *SysUser) error
		FindByID(ctx context.Context, id uint64) (*SysUser, error)
		FindByUsername(ctx context.Context, username string) (*SysUser, error)
		Update(ctx context.Context, u *SysUser) error
		Delete(ctx context.Context, id uint64) error
		FindList(ctx context.Context, page, size int, keyword string) ([]*SysUser, int64, error)
	}

	sysUserModel struct {
		db *gorm.DB
	}
)

func NewSysUserModel(db *gorm.DB) SysUserModel {
	return &sysUserModel{db: db}
}

func (m *sysUserModel) Insert(ctx context.Context, u *SysUser) error {
	return m.db.WithContext(ctx).Create(u).Error
}

func (m *sysUserModel) FindByID(ctx context.Context, id uint64) (*SysUser, error) {
	var u SysUser
	if err := m.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (m *sysUserModel) FindByUsername(ctx context.Context, username string) (*SysUser, error) {
	var u SysUser
	if err := m.db.WithContext(ctx).Where("username = ? AND deleted_at IS NULL", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// Update 仅更新昵称与状态, 避免覆盖密码.
func (m *sysUserModel) Update(ctx context.Context, u *SysUser) error {
	return m.db.WithContext(ctx).Model(&SysUser{}).
		Where("id = ? AND deleted_at IS NULL", u.ID).
		Updates(map[string]interface{}{"nickname": u.Nickname, "status": u.Status}).Error
}

// Delete 软删除(置 deleted_at).
func (m *sysUserModel) Delete(ctx context.Context, id uint64) error {
	return m.db.WithContext(ctx).Model(&SysUser{}).
		Where("id = ? AND deleted_at IS NULL", id).
		Update("deleted_at", gorm.Expr("NOW()")).Error
}

func (m *sysUserModel) FindList(ctx context.Context, page, size int, keyword string) ([]*SysUser, int64, error) {
	var list []*SysUser
	var total int64
	tx := m.db.WithContext(ctx).Model(&SysUser{}).Where("deleted_at IS NULL")
	if keyword != "" {
		tx = tx.Where("username LIKE ? OR nickname LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Offset((page - 1) * size).Limit(size).Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
