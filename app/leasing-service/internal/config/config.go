package config

import (
	"github.com/zeromicro/go-zero/rest"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Config 定义 leasing-service 的运行配置.
// 包含 go-zero REST 基础配置、MySQL、Redis 连接信息.
type Config struct {
	rest.RestConf
	MySQL gormx.MySQLConf  // MySQL 连接配置(leasing_db)
	Redis redisx.RedisConf // Redis 连接配置
}
