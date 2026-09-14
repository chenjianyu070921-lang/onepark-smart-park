package svc

import (
	"log"

	"onepark/app/visitor-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ServiceContext 持有 visitor-service 运行时的全局依赖.
// 包括配置、GORM 数据库连接和 Redis 客户端, 供 logic 层使用.
type ServiceContext struct {
	Config config.Config
	DB     *gormx.DB      // GORM MySQL 连接
	Redis  *redisx.Client // Redis 客户端
}

// NewServiceContext 根据配置初始化全局依赖.
// 若配置了 MySQL DSN 但初始化失败, 直接退出进程, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] visitor-service mysql data source is empty, db not initialized")
	}
	return &ServiceContext{
		Config: c,
		DB:     db,
		Redis:  redisx.NewClient(&c.Redis),
	}
}
