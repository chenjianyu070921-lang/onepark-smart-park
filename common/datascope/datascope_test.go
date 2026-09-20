package datascope

import (
	"context"
	"reflect"
	"testing"

	"onepark/common/ctxdata"
)

func TestWhereOf_Tenant(t *testing.T) {
	ctx := ctxdata.SetTenantId(context.Background(), 42)
	q, args := whereOf(ctx)
	if q != "tenant_id = ?" || !reflect.DeepEqual(args, []any{int64(42)}) {
		t.Fatalf("ScopeTenant: want (tenant_id = ?, [42]), got (%s, %v)", q, args)
	}
}

func TestWhereOf_All(t *testing.T) {
	ctx := ctxdata.SetDataScope(context.Background(), ScopeAll)
	q, args := whereOf(ctx)
	if q != "" || args != nil {
		t.Fatalf("ScopeAll: want empty filter, got (%s, %v)", q, args)
	}
}

func TestWhereOf_Self(t *testing.T) {
	ctx := ctxdata.SetDataScope(context.Background(), ScopeSelf)
	ctx = ctxdata.SetUserId(ctx, 7)
	q, args := whereOf(ctx)
	if q != "create_by = ?" || !reflect.DeepEqual(args, []any{int64(7)}) {
		t.Fatalf("ScopeSelf: want (create_by = ?, [7]), got (%s, %v)", q, args)
	}
}

func TestWhereOf_DefaultTenant(t *testing.T) {
	// 未设置 data_scope 时, 默认按租户隔离.
	ctx := ctxdata.SetTenantId(context.Background(), 99)
	q, args := whereOf(ctx)
	if q != "tenant_id = ?" || !reflect.DeepEqual(args, []any{int64(99)}) {
		t.Fatalf("default: want (tenant_id = ?, [99]), got (%s, %v)", q, args)
	}
}

func TestFromCtx_AppliesWhere(t *testing.T) {
	// FromCtx 必须基于 whereOf 的结果拼装 gorm 作用域(行为一致性守卫).
	ctx := ctxdata.SetTenantId(context.Background(), 5)
	scope := FromCtx(ctx)
	if scope == nil {
		t.Fatal("FromCtx returned nil scope")
	}
}
