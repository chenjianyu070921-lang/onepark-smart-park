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
}
