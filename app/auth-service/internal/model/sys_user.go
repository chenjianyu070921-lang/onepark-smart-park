package model

import (
	"context"

	"onepark/common/gormx"
)

// SysUser 系统用户表(对应 sys_db.sys_user). 仅保留鉴权所需字段.
// 说明: sys_user 已随 M6 多租户试点迁移(deploy/sql/m6_usermanage_tenant_pilot.sql / l2_tenant_id_migration.sql)
// 加入 tenant_id 列并回填默认园区(DefaultTenantId=1), 故鉴权产出 JWT 的 TenantId 取用户真实所属园区.
type SysUser struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Username string `gorm:"column:username" json:"username"`
	Password string `gorm:"column:password" json:"-"`
	Nickname string `gorm:"column:nickname" json:"nickname"`
	Status   int8   `gorm:"column:status" json:"status"`
	TenantId int64  `gorm:"column:tenant_id" json:"tenantId"` // 所属园区/租户ID, RBAC 数据隔离维度
}

func (SysUser) TableName() string { return "sys_user" }

// SysUserRole 用户-角色关联(对应 sys_db.sys_user_role).
type SysUserRole struct {
	UserID uint64 `gorm:"column:user_id"`
	RoleID uint64 `gorm:"column:role_id"`
}

func (SysUserRole) TableName() string { return "sys_user_role" }

// FindUserByUsername 按用户名查询未删除用户.
func FindUserByUsername(ctx context.Context, db *gormx.DB, username string) (*SysUser, error) {
	var u SysUser
	if err := db.WithContext(ctx).Where("username = ? AND deleted_at IS NULL", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// FindUserByID 按 ID 查询未删除用户(refresh 场景使用).
func FindUserByID(ctx context.Context, db *gormx.DB, id uint64) (*SysUser, error) {
	var u SysUser
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ListRoleIDs 查询用户绑定的角色 ID 列表.
func ListRoleIDs(ctx context.Context, db *gormx.DB, userID uint64) ([]uint64, error) {
	var ids []uint64
	if err := db.WithContext(ctx).Model(&SysUserRole{}).
		Where("user_id = ?", userID).Pluck("role_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}
