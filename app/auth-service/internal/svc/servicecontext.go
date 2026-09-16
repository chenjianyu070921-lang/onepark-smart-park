package svc

import (
	"fmt"

	"onepark/app/auth-service/internal/config"
	"onepark/common/gormx"
)

// ServiceContext 注入 JWT 配置与 sys_db 连接, 身份校验改为查询用户中心(DB).
type ServiceContext struct {
	Config     config.Config
	JwtSecret  string
	JwtExpire  int64
	JwtRefresh int64
	DB         *gormx.DB
}

// NewServiceContext 构建服务上下文: 校验 JWT 密钥并初始化 sys_db 连接.
func NewServiceContext(c config.Config) *ServiceContext {
	if c.JwtSecret == "" {
		panic("auth-service: JWT_SECRET is required, set environment variable JWT_SECRET")
	}
	expire := c.JwtExpire
	if expire <= 0 {
		expire = 7200
	}
	refresh := c.JwtRefresh
	if refresh <= 0 {
		refresh = 86400
	}

	db, err := gormx.NewDB(c.MySQL.DataSource)
	if err != nil {
		panic(fmt.Sprintf("auth-service: 初始化 MySQL(sys_db) 失败: %v", err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic(fmt.Sprintf("auth-service: 获取 SQLDB 失败: %v", err))
	}
	if c.MySQL.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(c.MySQL.MaxOpenConns)
	}
	if c.MySQL.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(c.MySQL.MaxIdleConns)
	}
	if err := sqlDB.Ping(); err != nil {
		panic(fmt.Sprintf("auth-service: sys_db 连通性检查失败: %v", err))
	}

	return &ServiceContext{
		Config:     c,
		JwtSecret:  c.JwtSecret,
		JwtExpire:  expire,
		JwtRefresh: refresh,
		DB:         db,
	}
}
