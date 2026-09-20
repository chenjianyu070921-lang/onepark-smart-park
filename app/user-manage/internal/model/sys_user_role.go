package model

import (
	"context"

	"gorm.io/gorm"
)

// SysUserRole 用户-角色关联(多对多).
type SysUserRole struct {
	ID     uint64 `gorm:"primaryKey;autoIncrement"`
	UserID uint64 `gorm:"column:user_id;type:bigint;not null;uniqueIndex:uk_user_role,priority:1"`
	RoleID uint64 `gorm:"column:role_id;type:bigint;not null;uniqueIndex:uk_user_role,priority:2"`
}

func (SysUserRole) TableName() string {
	return "sys_user_role"
}

type (
	SysUserRoleModel interface {
		Assign(ctx context.Context, userID, roleID uint64) error
		ListRoleIDs(ctx context.Context, userID uint64) ([]uint64, error)
		RemoveByUser(ctx context.Context, userID uint64) error
	}

	sysUserRoleModel struct {
		db *gorm.DB
	}
)

func NewSysUserRoleModel(db *gorm.DB) SysUserRoleModel {
	return &sysUserRoleModel{db: db}
}

func (m *sysUserRoleModel) Assign(ctx context.Context, userID, roleID uint64) error {
	ur := &SysUserRole{UserID: userID, RoleID: roleID}
	return m.db.WithContext(ctx).Create(ur).Error
}

func (m *sysUserRoleModel) ListRoleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := m.db.WithContext(ctx).Model(&SysUserRole{}).
		Where("user_id = ?", userID).Pluck("role_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (m *sysUserRoleModel) RemoveByUser(ctx context.Context, userID uint64) error {
	return m.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&SysUserRole{}).Error
}
