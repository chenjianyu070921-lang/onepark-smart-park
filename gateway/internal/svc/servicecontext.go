package svc

import (
	"log"

	authpb "onepark/proto/auth"
	userpb "onepark/proto/user"
	"onepark/common/redisx"
	"onepark/gateway/internal/config"
	"onepark/gateway/internal/discovery"
	"onepark/gateway/internal/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ServiceContext 持有网关运行时依赖.
type ServiceContext struct {
	Config     config.Config
	Gateway    *proxy.Gateway          // 前缀反向代理(统一转发到各业务服务)
	Redis      *redisx.Client           // 限流依赖; nil 表示未配置(限流降级放行)
	AuthClient authpb.AuthServiceClient // auth-service gRPC 客户端(Verify 校验入口); nil 表示鉴权未启用
	UserClient userpb.UserManageClient  // user-manage gRPC 客户端(CheckPermission RBAC 入口); nil 表示 RBAC 未启用
}

// NewServiceContext 构建网关上下文; 上游地址非法时直接退出.
func NewServiceContext(c config.Config) *ServiceContext {
	g, err := proxy.NewGateway(c)
	if err != nil {
		log.Fatalf("init gateway proxy failed: %v", err)
	}
	// 可选: 从 Nacos 配置中心动态拉取上游表(仅当 Nacos.Address 配置时).
	if c.Nacos.Address != "" {
		go discovery.StartWatch(c.Nacos, g)
	}
	// 可选: 限流 Redis; 仅当配置了 Addr 才初始化(未配置时限流降级放行).
	var rdb *redisx.Client
	if c.Redis.Addr != "" {
		rdb = redisx.NewClient(&c.Redis)
	}
	// 可选: auth-service gRPC 客户端; 仅当配置了 GrpcAddress 才初始化(未配置即鉴权未启用).
	// grpc.NewClient 为懒连接: 启动不阻塞, auth-service 不可达时首请求才报错(网关按鉴权失败 401 处理, 失败闭环).
	var authClient authpb.AuthServiceClient
	if c.Auth.GrpcAddress != "" {
		cc, derr := grpc.NewClient(c.Auth.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if derr != nil {
			log.Fatalf("init auth gRPC client failed: %v", derr)
		}
		authClient = authpb.NewAuthServiceClient(cc)
	}
	// 可选: user-manage gRPC 客户端; 仅当配置了 GrpcAddress 才初始化(RBAC CheckPermission 入口).
	// 同 auth 为懒连接: user-manage 不可达时首请求才报错(网关按鉴权失败 403 处理, 失败闭环).
	var userClient userpb.UserManageClient
	if c.User.GrpcAddress != "" {
		cc, derr := grpc.NewClient(c.User.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if derr != nil {
			log.Fatalf("init user-manage gRPC client failed: %v", derr)
		}
		userClient = userpb.NewUserManageClient(cc)
	}
	return &ServiceContext{
		Config:     c,
		Gateway:    g,
		Redis:      rdb,
		AuthClient: authClient,
		UserClient: userClient,
	}
}
