package config

import (
	"github.com/zeromicro/go-zero/rest"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 leasing-service 运行配置.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf // MySQL 连接配置(lease_contract 表)
	Redis redisx.RedisConf // Redis 连接配置
}
