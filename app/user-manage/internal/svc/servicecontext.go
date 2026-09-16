package svc

import (
	"fmt"

	"onepark/app/user-manage/internal/config"
	"onepark/app/user-manage/internal/model"
	"onepark/common/gormx"
)

// ServiceContext 持有 DB 与各领域 model, 供 logic 使用.
type ServiceContext struct {
	Config        config.Config
	DB            *gormx.DB
	UserModel     model.SysUserModel
	RoleModel     model.SysRoleModel
	MenuModel     model.SysMenuModel
	UserRoleModel model.SysUserRoleModel
	RoleMenuModel model.SysRoleMenuModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gormx.NewDB(c.MySQL.DataSource)
	if err != nil {
		panic(fmt.Sprintf("初始化 MySQL 失败: %v", err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic(fmt.Sprintf("获取 SQLDB 失败: %v", err))
	}
	if c.MySQL.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(c.MySQL.MaxOpenConns)
	}
	if c.MySQL.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(c.MySQL.MaxIdleConns)
	}
	if err := sqlDB.Ping(); err != nil {
		panic(fmt.Sprintf("MySQL 连通性检查失败: %v", err))
	}

	return &ServiceContext{
		Config:        c,
		DB:            db,
		UserModel:     model.NewSysUserModel(db),
		RoleModel:     model.NewSysRoleModel(db),
		MenuModel:     model.NewSysMenuModel(db),
		UserRoleModel: model.NewSysUserRoleModel(db),
		RoleMenuModel: model.NewSysRoleMenuModel(db),
	}
}
