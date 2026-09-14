package svc

import (
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/config"
	"onepark/app/device-service/internal/model"
)

type ServiceContext struct {
	Config          config.Config
	DB              *gorm.DB
	Redis           *redis.Redis
	ProductModel    model.ProductModel
	DeviceModel     model.DeviceModel
	ShadowModel     model.ShadowModel
	CommandLogModel model.CommandLogModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		panic(fmt.Sprintf("初始化 MySQL 失败: %v", err))
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(c.MySQL.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.MySQL.MaxIdleConns)

	if err := sqlDB.Ping(); err != nil {
		panic(fmt.Sprintf("MySQL 连通性检查失败: %v", err))
	}

	rds := redis.MustNewRedis(c.Redis)

	return &ServiceContext{
		Config:          c,
		DB:              db,
		Redis:           rds,
		ProductModel:    model.NewProductModel(db),
		DeviceModel:     model.NewDeviceModel(db),
		ShadowModel:     model.NewShadowModel(db),
		CommandLogModel: model.NewCommandLogModel(db),
	}
}
