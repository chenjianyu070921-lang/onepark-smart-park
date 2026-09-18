// Command grpcserver 启动 leasing-service 的 gRPC 入口(端口 9051)。
// 与 HTTP API(leasing.go) 为两个独立二进制: HTTP 面向前端/网关, gRPC 面向服务间调用。
//
// 与 HTTP 侧的差异: 本入口**不启动合同到期定时任务** —— 定时任务只能有一个执行方,
// 两个进程都跑会重复扫描(虽有 Redis 锁兜底, 但没必要制造这种竞争)。
// 定时任务归 HTTP 侧启动(见 leasing.go)。
package main

import (
	"flag"
	"log"
	"strings"

	leasingpb "onepark/proto/leasing"
	"onepark/app/leasing-service/internal/rpcserver"
	"onepark/app/leasing-service/internal/svc"
	"onepark/common/gormx"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/leasing-grpc.yaml", "the config file")

// rpcConfig 聚合 zrpc 配置与 MySQL 配置(同一 yaml 文件).
type rpcConfig struct {
	zrpc.RpcServerConf
	MySQL gormx.MySQLConf `json:",optional"`
}

func main() {
	flag.Parse()

	var c rpcConfig
	// conf.UseEnv(): go-zero 的 ${VAR} 展开默认关闭, 部署环境靠环境变量注入 DSN 时需要它
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 只装配 DB —— 本入口用到的两个 logic 都不碰 Redis。
	// 刻意不用 svc.NewServiceContext: 它在 Redis 探活失败时 log.Fatalf,
	// 而 gRPC 服务不应该因为缓存不可用就起不来。
	svcCtx := &svc.ServiceContext{}

	dsn := c.MySQL.DataSource
	// 环境变量缺失时 go-zero 会保留 ${VAR} 字面量, 直接拿去连库只会报出难懂的 "invalid DSN",
	// 这里先识别出来当作"未配置", 让日志说人话。
	if strings.Contains(dsn, "${") {
		log.Printf("[warn] leasing grpc mysql data source unresolved(%s), treat as empty", dsn)
		dsn = ""
	}
	if dsn != "" {
		db, err := gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		svcCtx.DB = db
		log.Printf("[info] leasing grpc mysql initialized")
	} else {
		// DB 为 nil 时两个接口会返回 ErrDepConnect —— 明确失败好过静默返回空数据
		log.Printf("[warn] leasing grpc mysql data source empty, db not initialized")
	}

	server := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		leasingpb.RegisterLeasingServiceServer(grpcServer, rpcserver.NewLeasingServer(svcCtx))
	})
	defer server.Stop()

	log.Printf("Starting leasing gRPC at %s", c.RpcServerConf.ListenOn)
	server.Start()
}
