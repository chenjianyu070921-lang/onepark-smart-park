package svc

import (
	"context"
	"log"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/billing-service/internal/config"
	"onepark/app/billing-service/internal/model"
	"onepark/common/redisx"
)

type ServiceContext struct {
	Config        config.Config
	DB            *gorm.DB // GORM MySQL 连接(组内公用库)
	Billing       *model.BillingModel
	EnergyReading *model.EnergyReadingModel
	Redis         *redisx.Client // Redis 客户端(月度自动出账分布式锁防重)
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	logx.Must(err)

	rdb := redisx.NewClient(&c.Redis)
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("[billing] redis ping failed: %v", err)
	}
	log.Printf("[billing] redis connected ok")

	return &ServiceContext{
		Config:        c,
		DB:            db,
		Billing:       model.NewBillingModel(db),
		EnergyReading: model.NewEnergyReadingModel(db),
		Redis:         rdb,
	}
}
