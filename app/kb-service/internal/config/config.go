package config

import "github.com/zeromicro/go-zero/rest"

// Config kb-service 运行配置.
type Config struct {
	rest.RestConf

	// MySQL 知识库库(kb_db): 文档 / 切片 / 问答日志 / 反馈
	MySQL struct {
		DataSource string
	}

	// Retrieval 检索参数
	Retrieval RetrievalConf

	// LLM 大模型(可选): 不配也能完整工作(退化为抽取式回答)
	LLM LLMConf `json:",optional"`
}

// RetrievalConf 检索配置.
type RetrievalConf struct {
	// Engine 检索引擎: keyword(默认, 本地零依赖) / vector(预留)
	Engine string `json:",default=keyword"`
	// TopK 取前几段
	TopK int `json:",default=3"`
	// MinScore 命中分数下限: 低于它视为"没有依据", 直接拒答
	MinScore float64 `json:",default=0.5"`
	// MaxScan 单次检索最多加载的切片数
	MaxScan int `json:",default=5000"`
}

// LLMConf 大模型配置.
type LLMConf struct {
	// Enable 总开关; false 或 APIKey 为空时走抽取式回答
	Enable  bool   `json:",default=false"`
	BaseURL string `json:",optional"`
	APIKey  string `json:",optional"`
	Model   string `json:",optional"`
	// Timeout 秒; 超时即回退抽取式
	Timeout int `json:",default=20"`
}
