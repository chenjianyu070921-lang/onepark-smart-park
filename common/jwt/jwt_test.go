package jwt

import "testing"

func TestGenerateParse(t *testing.T) {
	const secret = "test-secret"
	token, err := Generate(secret, 123, "1,2,3", 9, TypeAccess, 7200)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	claims, err := Parse(secret, token)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if claims.UserId != 123 {
		t.Errorf("UserId = %d, want 123", claims.UserId)
	}
	if claims.RoleIds != "1,2,3" {
		t.Errorf("RoleIds = %q, want %q", claims.RoleIds, "1,2,3")
	}
	if claims.TenantId != 9 {
		t.Errorf("TenantId = %d, want 9", claims.TenantId)
	}
	if claims.Type != TypeAccess {
		t.Errorf("Type = %q, want %q", claims.Type, TypeAccess)
	}
}

func TestParseWrongSecret(t *testing.T) {
	token, _ := Generate("secret-a", 1, "", 1, TypeAccess, 7200)
	if _, err := Parse("secret-b", token); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestParseRefreshRejectedAsAccess(t *testing.T) {
	token, _ := Generate("secret", 1, "", 1, TypeRefresh, 7200)
	claims, err := Parse("secret", token)
	if err != nil {
		t.Fatalf("Parse refresh failed: %v", err)
	}
	if claims.Type != TypeRefresh {
		t.Fatalf("Type = %q, want refresh", claims.Type)
	}
}
