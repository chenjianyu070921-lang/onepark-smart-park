// Package gormx 提供 GORM + MySQL 初始化封装, 供各业务服务复用.
// 当前仅封装 DSN 打开连接, 业务服务在 ServiceContext 中统一初始化并注入 logic.
package gormx

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MySQLConf 定义 MySQL 连接配置, 与 go-zero 配置加载保持一致.
type MySQLConf struct {
	DataSource string // DSN, 如 user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=True&loc=Local
}

// DB 是 *gorm.DB 的别名, 避免各服务直接依赖 gorm.io/gorm 版本.
type DB = gorm.DB

// NewDB 根据 MySQL DSN 初始化 GORM 数据库连接.
// 注意: gorm.Open 不会立即建立 TCP 连接, 首次查询时才会真正拨号.
func NewDB(dsn string) (*gorm.DB, error) {
	return gorm.Open(mysql.Open(dsn), &gorm.Config{})
}
