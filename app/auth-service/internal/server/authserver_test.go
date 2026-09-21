package server

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"

	"onepark/app/auth-service/internal/svc"
	"onepark/common/gormx"
	"onepark/common/jwt"
	authpb "onepark/proto/auth"
	commonpb "onepark/proto/common"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// grpcTestSecret 单元测试专用 JWT 密钥(非生产值).
const grpcTestSecret = "auth-grpc-test-secret-0123456789"

// startAuthGRPC 在本进程内启动真实 gRPC server(随机端口)并返回客户端.
// 目的: 不依赖任何部署配置(不改 auth-api.yaml / compose), 证明
// "proto 契约 + AuthServer 实现 + 复用 HTTP logic" 这条链路可用; 测试结束自动清理监听与连接.
func startAuthGRPC(t *testing.T, svcCtx *svc.ServiceContext) authpb.AuthServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听随机端口失败: %v", err)
	}
	gs := grpc.NewServer()
	authpb.RegisterAuthServiceServer(gs, NewAuthServer(svcCtx))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建立 gRPC 客户端失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return authpb.NewAuthServiceClient(conn)
}

// TestGRPCPing 健康探针: 不查库, 仅证明服务已注册并可达.
func TestGRPCPing(t *testing.T) {
	client := startAuthGRPC(t, &svc.ServiceContext{JwtSecret: grpcTestSecret})
	if _, err := client.Ping(context.Background(), &commonpb.Empty{}); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
}

// TestGRPCVerify 校验 gRPC Verify: 只接受未过期的 access 令牌, 且身份载荷经 proto 完整回传.
func TestGRPCVerify(t *testing.T) {
	svcCtx := &svc.ServiceContext{JwtSecret: grpcTestSecret}
	client := startAuthGRPC(t, svcCtx)

	access, err := jwt.Generate(grpcTestSecret, 100, "1,3", 5, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("签发 access 令牌失败: %v", err)
	}
	refresh, err := jwt.Generate(grpcTestSecret, 100, "1,3", 5, jwt.TypeRefresh, 3600)
	if err != nil {
		t.Fatalf("签发 refresh 令牌失败: %v", err)
	}

	cases := []struct {
		name      string
		token     string
		wantValid bool
	}{
		{"有效 access 令牌", access, true},
		{"refresh 令牌不被接受", refresh, false},
		{"空令牌", "", false},
		{"非 JWT 串", "not-a-jwt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := client.Verify(context.Background(), &authpb.VerifyReq{Token: c.token})
			if err != nil {
				t.Fatalf("Verify 不应返回 gRPC 错误: %v", err)
			}
			if resp.Valid != c.wantValid {
				t.Fatalf("valid = %v, want %v", resp.Valid, c.wantValid)
			}
			if !c.wantValid {
				return
			}
			if resp.UserId != 100 || resp.RoleIds != "1,3" || resp.TenantId != 5 {
				t.Fatalf("身份载荷未回传: %+v", resp)
			}
			if resp.ExpiresAt == 0 {
				t.Fatal("expires_at 未回传")
			}
		})
	}
}

// TestGRPCLoginThenVerifyIntegration gRPC 端到端: Login 签发 -> Verify 回环(需真实 sys_db).
// 未提供 AUTH_TEST_MYSQL_DSN / AUTH_TEST_USER / AUTH_TEST_PASS 时自动跳过, 不阻塞 CI.
func TestGRPCLoginThenVerifyIntegration(t *testing.T) {
	dsn := os.Getenv("AUTH_TEST_MYSQL_DSN")
	username := os.Getenv("AUTH_TEST_USER")
	password := os.Getenv("AUTH_TEST_PASS")
	if dsn == "" || username == "" || password == "" {
		t.Skip("未设置 AUTH_TEST_MYSQL_DSN/AUTH_TEST_USER/AUTH_TEST_PASS, 跳过 gRPC DB 集成用例")
	}

	db, err := gormx.NewDB(dsn)
	if err != nil {
		t.Fatalf("连接 sys_db 失败: %v", err)
	}
	client := startAuthGRPC(t, &svc.ServiceContext{
		JwtSecret: grpcTestSecret, JwtExpire: 7200, JwtRefresh: 86400, DB: db,
	})

	loginResp, err := client.Login(context.Background(), &authpb.LoginReq{Username: username, Password: password})
	if err != nil {
		t.Fatalf("gRPC Login 失败: %v", err)
	}
	if loginResp.Token == "" || loginResp.RefreshToken == "" {
		t.Fatal("gRPC Login 返回的 access/refresh 令牌不应为空")
	}

	verifyResp, err := client.Verify(context.Background(), &authpb.VerifyReq{Token: loginResp.Token})
	if err != nil || !verifyResp.Valid {
		t.Fatalf("gRPC 签发的令牌未通过 gRPC Verify: err=%v resp=%+v", err, verifyResp)
	}
	if verifyResp.UserId != loginResp.UserId {
		t.Fatalf("Verify userId=%d 与 Login userId=%d 不一致", verifyResp.UserId, loginResp.UserId)
	}

	// 错误密码: 当前 gRPC 层未做 errorx -> status code 映射, 断言以错误文本为准.
	// (已记录为一致性缺口: HTTP 层映射 M6-E-####, gRPC 层为 codes.Unknown + 文本)
	_, err = client.Login(context.Background(), &authpb.LoginReq{Username: username, Password: password + "_wrong"})
	if err == nil {
		t.Fatal("错误密码应被拒绝")
	}
	if st, _ := status.FromError(err); !strings.Contains(st.Message(), "invalid username or password") {
		t.Fatalf("期望凭证错误文本, 实际: %v", err)
	}
}
