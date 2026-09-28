package svc

import (
	"log"
	"strings"
	"time"

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
	// RecordPlans 录像计划数据访问层; nil 表示未配置 MySQL, 相关接口退化依赖错误.
	RecordPlans model.RecordPlanModel
	// StatusCache 摄像头在线状态缓存(#50); nil 表示未启用/Redis 不可用,
	// 读取侧会回退到 MySQL 中的 status, 不因缓存缺席而改变接口语义.
	StatusCache StatusCacheStore
	// DeadLetters 心跳消费死信台账; nil 表示未配置 MySQL, 坏消息退回"仅日志"(不可追溯但不停机).
	DeadLetters model.HeartbeatDLQModel
}

// NewServiceContext 构造依赖.
// MySQL 未配置时不初始化 DB(本地无中间件仍可启动); 已配置但连接失败则直接退出, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	svcCtx := &ServiceContext{Config: c}
	// Redis 与 MySQL 一样走"未配置即降级", 不能直接用 MustNewRedis:
	// 它在连不上时 log.Fatalf 退出进程, 而 yaml 里 ${REDIS_ADDR} 未展开(KI-12)时
	// 拿到的就是字面量 "${REDIS_ADDR}" —— 现象是"服务起不来", 而报错指向 Redis,
	// 真正的原因是环境变量没配, 排查方向完全跑偏。
	// 本服务 Redis 只承载心跳缓存这一层加速(状态事实来源仍是 MySQL),
	// 缺失时应按"缓存不可用"降级, 而不是让整个服务不启动。
	if host := unresolvedToEmpty(c.Redis.Host); host != "" {
		svcCtx.Redis = redis.MustNewRedis(c.Redis)
	} else {
		log.Printf("[warn] video-service redis host is empty, heartbeat status cache degraded (fallback to mysql status)")
	}
	// 心跳缓存 TTL 与离线判定阈值一致: 两者不一致会出现"缓存说在线、扫描判离线"的自我矛盾.
	// Redis 为 nil 时同样构造缓存对象: 它的所有方法对 nil 连接返回零值,
	// 调用方据此回退 MySQL 状态, 接口语义不变(见 StatusCache 注释).
	svcCtx.StatusCache = NewStatusCache(svcCtx.Redis,
		time.Duration(c.Heartbeat.OfflineAfterSeconds)*time.Second)

	if dsn := unresolvedToEmpty(c.MySQL.DataSource); dsn != "" {
		db, err := gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		svcCtx.DB = db
		svcCtx.Cameras = model.NewCameraModel(db)
		svcCtx.RecordPlans = model.NewRecordPlanModel(db)
		svcCtx.DeadLetters = model.NewHeartbeatDLQModel(db)
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
