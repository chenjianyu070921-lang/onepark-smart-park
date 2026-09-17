package svc

import (
	"log"
	"strings"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"
	"onepark/common/gormx"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// ServiceContext 持有 video-service 运行时的全局依赖.
// Cameras 抽成接口是为了让摄像头链路脱离 MySQL 可单测(见 internal/logic 单测).
type ServiceContext struct {
	Config  config.Config
	Redis   *redis.Redis
	DB      *gormx.DB         // GORM MySQL 连接(video_db)
	Cameras model.CameraModel // 摄像头数据访问层
}

// NewServiceContext 构造依赖.
// MySQL 未配置时不初始化 DB(本地无中间件仍可启动); 已配置但连接失败则直接退出, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	svcCtx := &ServiceContext{
		Config: c,
		Redis:  redis.MustNewRedis(c.Redis),
	}

	if dsn := unresolvedToEmpty(c.MySQL.DataSource); dsn != "" {
		db, err := gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		svcCtx.DB = db
		svcCtx.Cameras = model.NewCameraModel(db)
		log.Printf("[info] video-service mysql initialized, db=%s", databaseOf(dsn))
	} else {
		log.Printf("[warn] video-service mysql data source is empty, db not initialized")
	}
	return svcCtx
}

// unresolvedToEmpty 将未展开的环境变量占位符视为空值.
// go-zero 在环境变量缺失时会保留 ${VAR} 字面量, 直接拿去连 MySQL 会报 "invalid DSN".
func unresolvedToEmpty(v string) string {
	if strings.Contains(v, "${") {
		return ""
	}
	return v
}

// databaseOf 从 GORM DSN 中截取数据库名, 仅用于启动日志, 不建立连接.
func databaseOf(dsn string) string {
	// DSN 形如 user:pass@tcp(host:port)/dbname?charset=utf8mb4
	i := strings.LastIndex(dsn, "/")
	if i < 0 || i == len(dsn)-1 {
		return "<unknown>"
	}
	name := dsn[i+1:]
	if j := strings.IndexAny(name, "?&"); j >= 0 {
		name = name[:j]
	}
	return name
}
