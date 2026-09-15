package svc

import (
	"fmt"
	"os"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/shadow-service/internal/config"
	"onepark/app/shadow-service/internal/model"
)

type ServiceContext struct {
	Config      config.Config
	DB          *gorm.DB
	ShadowModel model.ShadowModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	// 安全约定: yaml 中只写 ${XXX} 占位, 真实地址/密码由环境变量注入
	dsn := os.ExpandEnv(c.MySQL.DataSource)
	if dsn == "" {
		panic("缺少 MySQL 配置: 请设置环境变量 MYSQL_DSN")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		panic(fmt.Sprintf("初始化 MySQL 失败: %v", err))
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(c.MySQL.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.MySQL.MaxIdleConns)

	if err := sqlDB.Ping(); err != nil {
		panic(fmt.Sprintf("MySQL 连通性检查失败: %v", err))
	}

	return &ServiceContext{
		Config:      c,
		DB:          db,
		ShadowModel: model.NewShadowModel(db),
	}
}
