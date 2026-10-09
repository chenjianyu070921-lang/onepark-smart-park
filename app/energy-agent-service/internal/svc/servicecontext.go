package svc

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"onepark/app/energy-agent-service/internal/config"
	"onepark/app/energy-agent-service/internal/llm"
	"onepark/app/energy-agent-service/internal/model"
)

type ServiceContext struct {
	Config config.Config

	// DB 数据库连接
	DB *gorm.DB
	// Reading 能耗读数查询(只读)
	Reading *model.EnergyReadingModel
	// Agent 智能体三张表的读写
	Agent *model.AgentModel
	// LLM 大模型客户端。没配 key 时这里是 mock 实现, 照样能跑
	LLM llm.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{
		// 只打慢查询和错误, 别把每条 SQL 都刷出来
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		panic("连不上数据库: " + err.Error())
	}

	return &ServiceContext{
		Config:  c,
		DB:      db,
		Reading: model.NewEnergyReadingModel(db),
		Agent:   model.NewAgentModel(db),
		LLM: llm.NewClient(llm.Config{
			Enable:  c.LLM.Enable,
			BaseURL: c.LLM.BaseURL,
			APIKey:  c.LLM.APIKey,
			Model:   c.LLM.Model,
			Timeout: c.LLM.Timeout,
		}),
	}
}
