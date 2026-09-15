package svc

import (
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/billing-service/internal/config"
	"onepark/app/billing-service/internal/model"
)

type ServiceContext struct {
	Config config.Config

	// DB 数据库连接, 全局共用
	DB *gorm.DB
	// Billing 计费规则和账单的读写方法
	Billing *model.BillingModel
	// EnergyReading 能耗读数表, 本服务只读(取出账要用的用量)
	EnergyReading *model.EnergyReadingModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	logx.Must(err)

	return &ServiceContext{
		Config:        c,
		DB:            db,
		Billing:       model.NewBillingModel(db),
		EnergyReading: model.NewEnergyReadingModel(db),
	}
}
