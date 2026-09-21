package logic

import (
	"context"
	"os"
	"testing"

	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/gormx"
	"onepark/common/jwt"
)

// testSecret 单元测试专用 JWT 密钥(非生产值).
const testSecret = "auth-service-unit-test-secret-0123456789"

// newTestCtx 构造不依赖 MySQL/Redis 的服务上下文.
// ServiceContext 字段全部可导出, 这里直接组装 —— 避免 NewServiceContext 的 DB 连通性 panic,
// 使纯 JWT 逻辑(Verify/Logout/Refresh 的令牌类型校验)可在无中间件环境跑单测.
func newTestCtx() *svc.ServiceContext {
	return &svc.ServiceContext{JwtSecret: testSecret, JwtExpire: 7200, JwtRefresh: 86400}
}

func mustToken(t *testing.T, userID int64, roleIDs string, tenantID int64, typ string, expire int64) string {
	t.Helper()
	tok, err := jwt.Generate(testSecret, userID, roleIDs, tenantID, typ, expire)
	if err != nil {
		t.Fatalf("签发 %s 令牌失败: %v", typ, err)
	}
	return tok
}

// TestVerifyLogic 校验纯 JWT 校验: 只接受未过期的 access 令牌, 且回显身份载荷.
func TestVerifyLogic(t *testing.T) {
	ctx := newTestCtx()
	otherSecretToken, err := jwt.Generate("another-secret", 42, "1,2", 7, jwt.TypeAccess, 3600)
	if err != nil {
		t.Fatalf("签发异密钥令牌失败: %v", err)
	}

	cases := []struct {
		name      string
		token     string
		wantValid bool
	}{
		{"空令牌", "", false},
		{"有效 access 令牌", mustToken(t, 42, "1,2", 7, jwt.TypeAccess, 3600), true},
		{"refresh 令牌不被接受", mustToken(t, 42, "1,2", 7, jwt.TypeRefresh, 3600), false},
		{"已过期令牌", mustToken(t, 42, "1,2", 7, jwt.TypeAccess, -10), false},
		{"异密钥签发", otherSecretToken, false},
		{"非 JWT 串", "not-a-jwt", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: c.token})
			if err != nil {
				t.Fatalf("Verify 不应返回错误(无效令牌用 valid=false 表达): %v", err)
			}
			if resp.Valid != c.wantValid {
				t.Fatalf("valid = %v, want %v", resp.Valid, c.wantValid)
			}
			if !c.wantValid {
				return
			}
			if resp.UserId != 42 || resp.RoleIds != "1,2" || resp.TenantId != 7 {
				t.Fatalf("身份载荷未回显: %+v", resp)
			}
			if resp.ExpiresAt == 0 {
				t.Fatal("expires_at 未回填")
			}
		})
	}
}

// TestLogoutLogicDegradesWithoutRedis 固化"Redis 未配置时注销降级"的既定语义:
// 空/非法/有效令牌均不应报错(黑名单降级为无操作, 见 common/tokenblk.Revoke).
func TestLogoutLogicDegradesWithoutRedis(t *testing.T) {
	ctx := newTestCtx() // Redis == nil
	cases := []struct {
		name  string
		token string
	}{
		{"空令牌直接返回", ""},
		{"非法令牌无需吊销", "not-a-jwt"},
		{"有效令牌降级放行", mustToken(t, 1, "1", 0, jwt.TypeAccess, 3600)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := NewLogoutLogic(context.Background(), ctx).Logout(&types.LogoutReq{Token: c.token}); err != nil {
				t.Fatalf("Redis 未配置时应降级为无操作, 实际报错: %v", err)
			}
		})
	}
}

// TestRefreshLogicRejectsNonRefreshToken 校验刷新入口的令牌类型闸门.
// 这三种输入都在"查询账号状态"之前被拒, 故无需 DB 即可断言 M6-E-0002.
func TestRefreshLogicRejectsNonRefreshToken(t *testing.T) {
	ctx := newTestCtx()
	cases := []struct {
		name  string
		token string
	}{
		{"空令牌", ""},
		{"非 JWT 串", "not-a-jwt"},
		{"access 令牌冒充 refresh", mustToken(t, 1, "1", 0, jwt.TypeAccess, 3600)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewRefreshLogic(context.Background(), ctx).Refresh(&types.RefreshReq{RefreshToken: c.token})
			if err == nil {
				t.Fatal("应被拒绝, 实际通过")
			}
			ce, ok := err.(*errorx.CodeError)
			if !ok || ce.Code != errorx.ErrUnauthorized {
				t.Fatalf("期望 %s, 实际 %v", errorx.ErrUnauthorized, err)
			}
		})
	}
}

// TestLoginLogicIntegration 登录链路集成用例(需真实 sys_db).
// 未提供 AUTH_TEST_MYSQL_DSN / AUTH_TEST_USER / AUTH_TEST_PASS 时自动跳过, 不阻塞 CI;
// 本地或联调环境带上真实凭据即可端到端验证"登录 -> 签发 -> Verify 回环".
func TestLoginLogicIntegration(t *testing.T) {
	dsn := os.Getenv("AUTH_TEST_MYSQL_DSN")
	username := os.Getenv("AUTH_TEST_USER")
	password := os.Getenv("AUTH_TEST_PASS")
	if dsn == "" || username == "" || password == "" {
		t.Skip("未设置 AUTH_TEST_MYSQL_DSN/AUTH_TEST_USER/AUTH_TEST_PASS, 跳过 DB 集成用例")
	}

	db, err := gormx.NewDB(dsn)
	if err != nil {
		t.Fatalf("连接 sys_db 失败: %v", err)
	}
	ctx := &svc.ServiceContext{JwtSecret: testSecret, JwtExpire: 7200, JwtRefresh: 86400, DB: db}
	login := NewLoginLogic(context.Background(), ctx)

	resp, err := login.Login(&types.LoginReq{Username: username, Password: password})
	if err != nil {
		t.Fatalf("正确密码应登录成功: %v", err)
	}
	if resp.Token == "" || resp.RefreshToken == "" {
		t.Fatal("access/refresh 双令牌不应为空")
	}

	// 自签发的 access 令牌必须能被 Verify 接受(纯 JWT 回环).
	verified, err := NewVerifyLogic(context.Background(), ctx).Verify(&types.VerifyReq{Token: resp.Token})
	if err != nil || !verified.Valid {
		t.Fatalf("自签令牌未通过 Verify: err=%v resp=%+v", err, verified)
	}
	if verified.UserId != resp.UserId {
		t.Fatalf("Verify 回显 userId=%d, 登录返回 userId=%d", verified.UserId, resp.UserId)
	}

	// 错误密码必须被拒, 且错误码为 M6-E-0002.
	_, err = login.Login(&types.LoginReq{Username: username, Password: password + "_wrong"})
	if err == nil {
		t.Fatal("错误密码应被拒绝")
	}
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrUnauthorized {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrUnauthorized, err)
	}
}

// TestVerifyLogicRespectsBlacklist 已迁至 logic_session_test.go:
// 改用 miniredis 作为内存 Redis, 使"吊销闭环"用例不再依赖外部 Redis、必跑(见下).
