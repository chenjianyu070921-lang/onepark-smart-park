package svc

import (
	"fmt"

	"onepark/app/auth-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ServiceContext 注入 JWT 配置与 sys_db 连接, 身份校验改为查询用户中心(DB).
type ServiceContext struct {
	Config     config.Config
	JwtSecret  string
	JwtExpire  int64
	JwtRefresh int64
	DB         *gormx.DB
	Redis      *redisx.Client // 令牌注销黑名单; nil 表示未配置(注销降级为无操作)
}

// NewServiceContext 构建服务上下文: 初始化 sys_db 连接.
// 注: JWT 密钥的空值策略统一由启动期 validateJwtSecret 把关(非本地为空即 fatal, 本地 dev/test 放行),
// 此处不再重复无条件 panic —— 否则"本地放行"不可达, 即遗留台账 #11 所指的"放行形同虚设".
func NewServiceContext(c config.Config) *ServiceContext {
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

	// 可选: 令牌注销黑名单 Redis; 仅当配置了 Addr 才初始化(未配置时注销降级为无操作).
	var rdb *redisx.Client
	if c.Redis.Addr != "" {
		rdb = redisx.NewClient(&c.Redis)
	}

	return &ServiceContext{
		Config:     c,
		JwtSecret:  c.JwtSecret,
		JwtExpire:  expire,
		JwtRefresh: refresh,
		DB:         db,
		Redis:      rdb,
	}
}
