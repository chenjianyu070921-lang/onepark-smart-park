package svc

import (
	"log"
	"strings"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/model"
	"onepark/common/gormx"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
)

// ServiceContext 持有 access-control-service 运行时的全局依赖.
// OperateLogs/DeviceRPC 抽成接口是为了让远程开门链路脱离 MySQL/M1 可单测(见 internal/logic 单测).
type ServiceContext struct {
	Config      config.Config
	Redis       *redis.Redis
	DB          *gormx.DB                    // GORM MySQL 连接(access_db)
	OperateLogs model.OperateLogModel        // 开门操作审计
	Permissions model.PermissionModel        // 门禁权限(#45 授权 / #46 撤销)
	Records     model.RecordModel            // 通行记录(#48 查询)
	DeviceRPC   devicepb.DeviceServiceClient // M1 device gRPC(远程开门); 未配置时为 nil
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
		svcCtx.OperateLogs = model.NewOperateLogModel(db)
		svcCtx.Permissions = model.NewPermissionModel(db)
		svcCtx.Records = model.NewRecordModel(db)
		log.Printf("[info] access-control-service mysql initialized, db=%s", databaseOf(dsn))
	} else {
		log.Printf("[warn] access-control-service mysql data source is empty, db not initialized")
	}

	// M1 设备 gRPC 客户端: 未配置 Endpoints/Target/Etcd 时为 nil, 远程开门直接报错而非降级静默.
	if len(c.DeviceRPC.Endpoints) > 0 || c.DeviceRPC.Target != "" || len(c.DeviceRPC.Etcd.Hosts) > 0 {
		svcCtx.DeviceRPC = devicepb.NewDeviceServiceClient(zrpc.MustNewClient(c.DeviceRPC).Conn())
	} else {
		log.Printf("[warn] access-control-service device rpc not configured, remote open door unavailable")
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
