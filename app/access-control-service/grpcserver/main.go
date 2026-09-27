// Command grpcserver 启动 access-control-service 的 gRPC 入口(端口 9010), 提供门禁控制面.
//
// 与 HTTP API(accesscontrol.go) 为两个独立二进制: HTTP 面向前端/BFF, gRPC 面向服务间调用.
// 端口避让: HTTP 8010 / gRPC 9010 —— 与 alarm(8009/9009) 同构, 避免与 M1 device(9001) 撞端口.
package main

import (
	"flag"
	"log"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/rpcserver"
	"onepark/app/access-control-service/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"

	"onepark/common/gormx"
	accesspb "onepark/proto/access"
)

var configFile = flag.String("f", "etc/accesscontrol-grpc.yaml", "the config file")

// rpcConfig 聚合 zrpc 配置与中间件配置(同一 yaml 文件).
// 与 config.Config 分开定义: gRPC 进程不需要 rest.RestConf(端口/超时等 HTTP 语义).
type rpcConfig struct {
	zrpc.RpcServerConf
	Redis      redis.RedisConf       `json:",optional"`
	MySQL      gormx.MySQLConf       `json:",optional"` // access_db(权限 / 审计 / 通行记录)
	DeviceRPC  zrpc.RpcClientConf    `json:",optional"` // M1 device-service(远程开门下发命令)
	RemoteOpen config.RemoteOpenConf `json:",optional"` // 远程开门设备白名单兜底
}

func main() {
	flag.Parse()

	var c rpcConfig
	// conf.UseEnv() 必填: ${VAR} 展开默认关闭, 不启用则 DSN 恒为字面量.
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 复用 HTTP 侧同一套装配逻辑: 未配置 MySQL 时 Permissions/Records 为 nil,
	// gRPC 仍可启动并提供探活, 权限校验按 Unavailable 拒绝(fail-closed).
	svcCtx := svc.NewServiceContext(config.Config{
		Redis:      c.Redis,
		MySQL:      c.MySQL,
		DeviceRPC:  c.DeviceRPC,
		RemoteOpen: c.RemoteOpen,
	})

	server := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		accesspb.RegisterAccessControlServiceServer(grpcServer, rpcserver.NewAccessServer(svcCtx))
	})
	defer server.Stop()

	log.Printf("Starting access-control gRPC at %s", c.RpcServerConf.ListenOn)
	server.Start()
}
