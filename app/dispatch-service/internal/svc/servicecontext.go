package svc

import (
	"log"

	"onepark/app/dispatch-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ServiceContext 持有 dispatch-service 运行时的全局依赖.
type ServiceContext struct {
	Config config.Config
	DB     *gormx.DB       // GORM MySQL 连接(dispatch_task)
	Redis  *redisx.Client  // Redis 客户端
}

// NewServiceContext 根据配置初始化全局依赖.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] dispatch mysql data source empty, db not initialized")
	}
	redis := redisx.NewClient(&c.Redis)
	return &ServiceContext{
		Config: c,
		DB:     db,
		Redis:  redis,
	}
}
