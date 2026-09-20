package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
)

type Config struct {
	rest.RestConf

	// MySQL 组内公用库 onepark-smart-park
	MySQL struct {
		DataSource string
	}

	// Redis 缓存: 计费规则
	Redis redis.RedisConf

	// BillJob 定时出账(接口61 的定时触发): 每月固定日子给上个自然月出账
	BillJob struct {
		Enable bool `json:",default=true"`
		// DayOfMonth 每月几号出账, 只在 1~28 之间有效(29~31 有的月份没有)
		DayOfMonth int `json:",default=1"`
	}
}
