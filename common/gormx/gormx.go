// Package gormx 提供 GORM + MySQL 初始化封装, 供各业务服务复用.
// 当前仅封装 DSN 打开连接, 业务服务在 ServiceContext 中统一初始化并注入 logic.
package gormx

import (
	"errors"

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

// IsDuplicateKey 判断错误是否为唯一键冲突(并发插入竞态的兜底判定).
// GORM 已将 MySQL 1062 归一为 gorm.ErrDuplicatedKey; 这里再 errors.Is 一层以兼容包装/翻译.
// 配合各业务表"生成列 + 唯一索引"的并发去重防线(见 deploy/sql/m2_p2_*_unique.sql),
// 使"查→写"非原子路径在竞态下可幂等回退而非产生重复生效记录.
func IsDuplicateKey(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
