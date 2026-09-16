// Command grpcserver 启动 workorder 服务的 gRPC 入口(端口 9091), 供 M5 大屏聚合调用.
// 与 HTTP API(main.go) 为两个独立二进制: HTTP 面向前端/BFF, gRPC 面向服务间聚合.
package main

import (
	"flag"
	"log"

	"onepark/app/workorder-service/internal/rpcserver"
	"onepark/common/gormx"
	workorderpb "onepark/proto/workorder"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "../etc/workorder-grpc.yaml", "the config file")

// rpcConfig 聚合 zrpc 配置与 MySQL 配置(同一 yaml 文件).
type rpcConfig struct {
	zrpc.RpcServerConf
	MySQL gormx.MySQLConf `json:",optional"`
}

func main() {
	flag.Parse()

	var c rpcConfig
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 初始化 MySQL(未配置 DSN 时 DB 为 nil, gRPC 仍可提供探活与空聚合).
	var db *gormx.DB
	if c.MySQL.DataSource != "" {
		var err error
		db, err = gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
	} else {
		log.Printf("[warn] workorder grpc mysql data source empty, db nil")
	}

	server := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		workorderpb.RegisterWorkorderServiceServer(grpcServer, rpcserver.NewWorkorderServer(db))
	})
	defer server.Stop()

	log.Printf("Starting workorder gRPC at %s", c.RpcServerConf.ListenOn)
	server.Start()
}
