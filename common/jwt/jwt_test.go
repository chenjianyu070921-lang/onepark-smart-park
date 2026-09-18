package jwt

import (
	"testing"
	"time"
)

// TestGenerateParseRoundTrip 验证签发->校验闭环: 身份字段(user_id/role_ids/tenant_id/type)不丢.
func TestGenerateParseRoundTrip(t *testing.T) {
	secret := "unit-test-secret"
	tok, err := Generate(secret, 100, "1,2", 5, TypeAccess, 3600)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	c, err := Parse(secret, tok)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if c.UserId != 100 {
		t.Errorf("UserId want 100, got %d", c.UserId)
	}
	if c.TenantId != 5 {
		t.Errorf("TenantId want 5, got %d", c.TenantId)
	}
	if c.RoleIds != "1,2" {
		t.Errorf("RoleIds want '1,2', got %q", c.RoleIds)
	}
	if c.Type != TypeAccess {
		t.Errorf("Type want %q, got %q", TypeAccess, c.Type)
	}
}

// TestParseWrongSecret 验证密钥不一致时校验失败(防伪造 token).
func TestParseWrongSecret(t *testing.T) {
	tok, err := Generate("secret-1", 1, "", 1, TypeAccess, 3600)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if _, err := Parse("secret-2", tok); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

// TestParseExpired 验证过期令牌被拒.
func TestParseExpired(t *testing.T) {
	// expireSeconds 为负 -> ExpiresAt 已早于当前时间.
	tok, err := Generate("secret", 1, "", 1, TypeAccess, -10)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	// 给一点时间确保已过期.
	time.Sleep(5 * time.Millisecond)
	if _, err := Parse("secret", tok); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestParseTampered 验证篡改载荷会被签名校验拦截.
func TestParseTampered(t *testing.T) {
	tok, _ := Generate("secret", 1, "", 1, TypeAccess, 3600)
	// 把最后一段(签名)替换, 模拟伪造.
	tampered := tok + "tampered"
	if _, err := Parse("secret", tampered); err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}
