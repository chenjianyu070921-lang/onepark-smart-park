package svc

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/config"
	"onepark/app/device-service/internal/model"
	"onepark/common/kafka"
	"onepark/common/mqtt"
	"onepark/common/tdengine"

	"github.com/zeromicro/go-zero/core/logx"
)

// CommandDownlink 指令下行通道抽象, 便于单测替换; *mqtt.Client 实现该接口.
type CommandDownlink interface {
	Publish(ctx context.Context, topic string, qos byte, payload []byte) error
}

type ServiceContext struct {
	Config   config.Config
	DB       *gorm.DB
	Redis    *redis.Redis
	Producer *kafka.Producer
	// Downlink 指令下行(MQTT); 为 nil 时指令仅落库, 不阻断受理
	Downlink CommandDownlink
	// TDengine 遥测时序落库; 为 nil 时跳过写时序库
	TDengine        *tdengine.Client
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

	ctx := &ServiceContext{
		Config:          c,
		DB:              db,
		Redis:           rds,
		Producer:        kafka.NewProducer(brokers),
		TDengine:        tdengine.NewClient(c.TDengine),
		ProductModel:    model.NewProductModel(db),
		DeviceModel:     model.NewDeviceModel(db),
		ShadowModel:     model.NewShadowModel(db),
		CommandLogModel: model.NewCommandLogModel(db),
	}

	// MQTT 下行通道: 连接失败只降级不 panic, 保证服务在 EMQX 未就绪时仍能启动
	c.MQTT.Broker = os.ExpandEnv(c.MQTT.Broker)
	// 环境变量未注入时 yaml 占位符可能未被替换, 视为未配置
	if strings.Contains(c.MQTT.Broker, "${") {
		c.MQTT.Broker = ""
	}
	if c.MQTT.Broker != "" {
		cli, err := mqtt.NewClient(c.MQTT)
		if err != nil {
			logx.Errorf("MQTT 连接失败, 指令下行降级为仅落库: %v", err)
		} else {
			logx.Infof("MQTT 下行通道已就绪: broker=%s", c.MQTT.Broker)
			ctx.Downlink = cli
		}
	} else {
		logx.Errorf("未配置 MQTT broker, 指令下行降级为仅落库")
	}
	ctx.Config.MQTT = c.MQTT

	if ctx.TDengine == nil {
		logx.Errorf("未配置 TDengine REST 地址, 遥测不落时序库")
	}

	return ctx
}

// Close 释放外部资源.
func (s *ServiceContext) Close() {
	if s.Producer != nil {
		_ = s.Producer.Close()
	}
	if cli, ok := s.Downlink.(*mqtt.Client); ok && cli != nil {
		cli.Close()
	}
}
