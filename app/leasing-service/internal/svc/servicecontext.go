package svc

import (
	"context"
	"log"
	"time"

	"onepark/app/leasing-service/internal/config"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ServiceContext 持有 leasing-service 运行时的全局依赖.
type ServiceContext struct {
	Config config.Config
	DB     *gormx.DB      // GORM MySQL 连接(leasing_db)
	Redis  *redisx.Client // Redis 客户端
}

// NewServiceContext 根据配置初始化全局依赖.
// 初始化后主动探活一次, 使启动日志能明确反映 MySQL / Redis 连接状态.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		opened, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("[leasing] init mysql failed: %v", err)
		}
		sqlDB, err := opened.DB()
		if err != nil {
			log.Fatalf("[leasing] get sql.DB failed: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			log.Fatalf("[leasing] mysql ping failed: %v", err)
		}
		log.Printf("[leasing] mysql connected ok")
		db = opened
	} else {
		log.Printf("[warn] leasing-service mysql data source is empty, db not initialized")
	}

	rdb := redisx.NewClient(&c.Redis)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("[leasing] redis ping failed: %v", err)
	}
	log.Printf("[leasing] redis connected ok")

	return &ServiceContext{
		Config: c,
		DB:     db,
		Redis:  rdb,
	}
}
