package svc

import (
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/energy-analysis-service/internal/config"
	"onepark/app/energy-analysis-service/internal/model"
)

type ServiceContext struct {
	Config config.Config

	// DB 数据库连接, 全局共用
	DB *gorm.DB
	// EnergyReading 能耗读数表的查询方法(本服务只读不写)
	EnergyReading *model.EnergyReadingModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	logx.Must(err)

	return &ServiceContext{
		Config:        c,
		DB:            db,
		EnergyReading: model.NewEnergyReadingModel(db),
	}
}
