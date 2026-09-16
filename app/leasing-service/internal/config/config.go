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

	// JwtSecret HTTP 接口鉴权密钥; 为空时放行(M6 认证服务就绪前保持可联调, 中间件会打告警日志)
	JwtSecret string `json:",env=JWT_SECRET,optional"`
}
