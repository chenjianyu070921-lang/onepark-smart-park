// Package svc 服务上下文: 一处装配依赖, 便于替换与测试.
package svc

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/kb-service/internal/answer"
	"onepark/app/kb-service/internal/config"
	"onepark/app/kb-service/internal/llm"
	"onepark/app/kb-service/internal/model"
	"onepark/app/kb-service/internal/retriever"
)

// ServiceContext 全局依赖.
type ServiceContext struct {
	Config    config.Config
	DB        *gorm.DB
	KB        *model.KBModel
	Retriever retriever.Retriever
	Answer    *answer.Builder
	LLMName   string // 空 = 未启用大模型(抽取式)
}

// NewServiceContext 装配依赖.
func NewServiceContext(c config.Config) *ServiceContext {
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	logx.Must(err)

	kb := model.NewKBModel(db)
	// 检索器: 用"载入切片"的闭包注入数据源, 检索逻辑本身不碰 DB(便于单测)
	load := func(ctx context.Context, tenantID uint64, limit int) ([]retriever.Chunk, error) {
		rows, err := kb.ScanChunks(ctx, tenantID, limit)
		if err != nil {
			return nil, err
		}
		out := make([]retriever.Chunk, 0, len(rows))
		for _, r := range rows {
			out = append(out, retriever.Chunk{
				ID: r.ID, DocumentID: r.DocumentID, Seq: r.Seq,
				Heading: r.Heading, Content: r.Content, Title: r.Title, Source: r.Source,
			})
		}
		return out, nil
	}
	engine := retriever.NewKeyword(load, c.Retrieval.MinScore, c.Retrieval.MaxScan)

	llmClient := llm.New(c.LLM.Enable, llm.Options{
		BaseURL: c.LLM.BaseURL,
		APIKey:  c.LLM.APIKey,
		Model:   c.LLM.Model,
		Timeout: time.Duration(c.LLM.Timeout) * time.Second,
	})
	llmName := ""
	if llmClient != nil {
		llmName = llmClient.Name()
		logx.Infof("[kb] 大模型已启用: %s（生成式回答；失败会自动回退抽取式）", llmName)
	} else {
		logx.Infof("[kb] 大模型未启用 -> 抽取式回答（离线可用；配 KB_LLM_API_KEY 与 LLM.Enable=true 可升级）")
	}

	return &ServiceContext{
		Config:    c,
		DB:        db,
		KB:        kb,
		Retriever: engine,
		Answer:    answer.New(llmClient),
		LLMName:   llmName,
	}
}
