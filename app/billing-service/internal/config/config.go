package config

import (
	"github.com/zeromicro/go-zero/rest"
	"onepark/common/redisx"
)

// Config 定义 billing-service 的运行配置.
type Config struct {
	rest.RestConf

	// MySQL 组内公用库 onepark-smart-park
	MySQL struct {
		DataSource string
	}

	// Redis 客户端: 月度自动出账的分布式锁防重(复用 leasing 模式).
	Redis redisx.RedisConf

	// DefaultTenantId 自动出账时能耗读数未携带租户(历史/M4 未回填), 统一落到该兜底园区.
	DefaultTenantId int64 `json:",default=1"`

	// MonthlyCron 月度自动出账定时任务.
	MonthlyCron MonthlyCronConf
}

// MonthlyCronConf 月度自动出账配置.
type MonthlyCronConf struct {
	Enabled bool   `json:",default=false"`     // 默认关闭, 部署时显式开启
	Spec    string `json:",default=0 2 1 * *"` // 每月 1 日 02:02 出上月账
}
