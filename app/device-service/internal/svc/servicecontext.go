package svc

import (
	"fmt"
	"os"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/config"
	"onepark/app/device-service/internal/model"
	"onepark/common/kafka"
)

type ServiceContext struct {
	Config          config.Config
	DB              *gorm.DB
	Redis           *redis.Redis
	Producer        *kafka.Producer
	ProductModel    model.ProductModel
	DeviceModel     model.DeviceModel
	ShadowModel     model.ShadowModel
	CommandLogModel model.CommandLogModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	// 安全约定: yaml 中只写 ${XXX} 占位, 真实地址/密码由环境变量注入, 禁止明文入库
	dsn := os.ExpandEnv(c.MySQL.DataSource)
	if dsn == "" {
		panic("缺少 MySQL 配置: 请设置环境变量 MYSQL_DSN")
	}

	c.Redis.Host = os.ExpandEnv(c.Redis.Host)
	if c.Redis.Host == "" {
		panic("缺少 Redis 配置: 请设置环境变量 REDIS_HOST")
	}

	brokers := os.ExpandEnv(c.KafkaBrokers)
	if brokers == "" {
		brokers = "localhost:9092"
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

	rds := redis.MustNewRedis(c.Redis)

	return &ServiceContext{
		Config:          c,
		DB:              db,
		Redis:           rds,
		Producer:        kafka.NewProducer(brokers),
		ProductModel:    model.NewProductModel(db),
		DeviceModel:     model.NewDeviceModel(db),
		ShadowModel:     model.NewShadowModel(db),
		CommandLogModel: model.NewCommandLogModel(db),
	}
}
