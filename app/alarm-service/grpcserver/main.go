// Command grpcserver 启动 alarm-service 的 gRPC 入口(端口 9009), 供 M5 大屏调用 GetActiveAlarms.
// 与 HTTP API(alarm.go) 为两个独立二进制: HTTP 面向前端/BFF, gRPC 面向服务间聚合.
// Kafka 消费 goroutine 由 HTTP 侧启动, 避免两个进程重复消费.
package main

import (
	"flag"
	"log"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/rpcserver"
	"onepark/common/gormx"
	alarmpb "onepark/proto/alarm"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/alarm-grpc.yaml", "the config file")

// rpcConfig 聚合 zrpc 配置与 MySQL 配置(同一 yaml 文件).
type rpcConfig struct {
	zrpc.RpcServerConf
	MySQL gormx.MySQLConf `json:",optional"`
}

func main() {
	flag.Parse()

	var c rpcConfig
	// conf.UseEnv() 必填: ${VAR} 展开默认关闭, 不启用则 DSN 恒为字面量.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// MySQL 未配置时 Alarms 为 nil, gRPC 仍可启动并提供探活与空聚合.
	var alarms model.AlarmModel
	if dsn := unresolvedToEmpty(c.MySQL.DataSource); dsn != "" {
		db, err := gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		alarms = model.NewAlarmModel(db)
		log.Printf("[info] alarm grpc mysql initialized")
	} else {
		log.Printf("[warn] alarm grpc mysql data source empty, alarms nil")
	}

	server := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		alarmpb.RegisterAlarmServiceServer(grpcServer, rpcserver.NewAlarmServer(alarms))
	})
	defer server.Stop()

	log.Printf("Starting alarm gRPC at %s", c.RpcServerConf.ListenOn)
	server.Start()
}

// unresolvedToEmpty 将未展开的环境变量占位符视为空值(go-zero 在环境变量缺失时保留 ${VAR} 字面量).
func unresolvedToEmpty(v string) string {
	for i := 0; i < len(v)-1; i++ {
		if v[i] == '$' && v[i+1] == '{' {
			return ""
		}
	}
	return v
}
