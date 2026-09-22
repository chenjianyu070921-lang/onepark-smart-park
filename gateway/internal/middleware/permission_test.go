package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"onepark/common/ctxdata"
	"onepark/common/redisx"
	userpb "onepark/proto/user"

	"github.com/alicebob/miniredis/v2"
	"google.golang.org/grpc"
)

// fakeUserClient 是 userpb.UserManageClient 的测试替身: 仅实现 CheckPermission, 其余方法由内嵌接口兜底(测试中不触发).
// 以 granted 权限集合模拟"某角色拥有的权限", 入参 permission 在集合内即视为 Allowed=true.
type fakeUserClient struct {
	userpb.UserManageClient
	granted   map[string]bool
	callCount int
}

func (f *fakeUserClient) CheckPermission(_ context.Context, in *userpb.CheckPermissionReq, _ ...grpc.CallOption) (*userpb.CheckPermissionResp, error) {
	f.callCount++
	return &userpb.CheckPermissionResp{Allowed: f.granted[in.Permission]}, nil
}

// TestMatchPermission 验证路由->权限注册表映射正确(对齐 deploy/sql/sys.sql 的 sys_menu.permission, resource:action 粒度).
func TestMatchPermission(t *testing.T) {
	cases := []struct {
		method, path, wantPerm string
		wantOk                 bool
	}{
		// user-manage(原注册表)
		{http.MethodPost, "/api/users", "user:write", true},
		{http.MethodPut, "/api/users", "user:write", true},
		{http.MethodDelete, "/api/users/123", "user:write", true},
		{http.MethodGet, "/api/users", "user:read", true},
		{http.MethodGet, "/api/users/456", "user:read", true},
		{http.MethodPost, "/api/users/roles", "user:write", true},
		{http.MethodPost, "/api/roles", "role:write", true},
		{http.MethodGet, "/api/roles", "role:read", true},
		{http.MethodPost, "/api/menus", "menu:write", true},
		{http.MethodPost, "/api/roles/menus", "menu:write", true},
		// access-control(保安=access:read; super_admin=access:write)
		{http.MethodGet, "/api/access/records", "access:read", true},
		{http.MethodPost, "/api/access/grant", "access:write", true},
		{http.MethodDelete, "/api/access/revoke", "access:write", true},
		// alarm(保安=alarm:read/alarm:confirm; super_admin=alarm:write)
		{http.MethodGet, "/api/alarms", "alarm:read", true},
		{http.MethodGet, "/api/alarm/active", "alarm:read", true},
		{http.MethodGet, "/api/alarm/rules", "alarm:read", true},
		{http.MethodPut, "/api/alarm/:id/status", "alarm:confirm", true}, // 最长前缀优先
		{http.MethodPost, "/api/alarm/rule", "alarm:write", true},
		{http.MethodPut, "/api/alarm/rule/:id", "alarm:write", true},
		// workorder(物业经理=workorder:read/write)
		{http.MethodGet, "/api/workorder/1", "workorder:read", true},
		{http.MethodGet, "/api/workorders", "workorder:read", true},
		{http.MethodPost, "/api/workorder", "workorder:write", true},
		{http.MethodPut, "/api/workorder/1/assign", "workorder:write", true},
		// parking(物业经理=parking:read/write)
		{http.MethodGet, "/api/parking/active", "parking:read", true},
		{http.MethodGet, "/api/parking/records", "parking:read", true},
		{http.MethodPost, "/api/parking/entry", "parking:write", true},
		{http.MethodPost, "/api/parking/monthly-card", "parking:write", true},
		// 未覆盖服务: 默认放行
		{http.MethodPost, "/api/visitor", "", false},
		{http.MethodGet, "/api/devices", "", false},
		{http.MethodPost, "/api/lease", "", false},
		{http.MethodPost, "/api/permissions/check", "", false}, // 校验端点自身不拦截
	}
	for _, c := range cases {
		perm, ok := matchPermission(c.method, c.path)
		if ok != c.wantOk || perm != c.wantPerm {
			t.Errorf("matchPermission(%s %s) = (%q,%v), want (%q,%v)", c.method, c.path, perm, ok, c.wantPerm, c.wantOk)
		}
	}
}

// TestPermission 验证 RBAC 中间件基础行为: 有权放行 / 无权 403 / 未覆盖默认放行 / 缺身份 fail-closed.
func TestPermission(t *testing.T) {
	newReq := func(method, path string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set(ctxdata.CtxUserId, "1")
		r.Header.Set(ctxdata.CtxRoleIds, "1")
		return r
	}

	// 1) 有权限 -> 200
	rec := httptest.NewRecorder()
	Permission(&fakeUserClient{granted: map[string]bool{"user:write": true}}, nil)(downstream())(rec, newReq(http.MethodPost, "/api/users"))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed: want 200, got %d", rec.Code)
	}

	// 2) 无权限 -> 403
	rec2 := httptest.NewRecorder()
	Permission(&fakeUserClient{granted: map[string]bool{}}, nil)(downstream())(rec2, newReq(http.MethodPost, "/api/users"))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("denied: want 403, got %d", rec2.Code)
	}

	// 3) 未覆盖路由(其余服务)默认放行, 不调 gRPC
	rec3 := httptest.NewRecorder()
	neverCalled := &fakeUserClient{granted: map[string]bool{}}
	Permission(neverCalled, nil)(downstream())(rec3, newReq(http.MethodPost, "/api/visitor"))
	if rec3.Code != http.StatusOK {
		t.Fatalf("uncovered: want 200, got %d", rec3.Code)
	}
	if neverCalled.callCount != 0 {
		t.Fatalf("uncovered route should not call CheckPermission, got %d", neverCalled.callCount)
	}

	// 4) 缺身份(理论不会到这, Auth 已拦截) -> 403 fail-closed
	rec4 := httptest.NewRecorder()
	noID := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	noID.Header.Set(ctxdata.CtxRoleIds, "1")
	Permission(&fakeUserClient{granted: map[string]bool{"user:write": true}}, nil)(downstream())(rec4, noID)
	if rec4.Code != http.StatusForbidden {
		t.Fatalf("no identity: want 403, got %d", rec4.Code)
	}
}

// TestPermissionBusinessRoles 验证"不同角色看到不同": 保安/物业经理/super_admin 对同接口结果不同.
func TestPermissionBusinessRoles(t *testing.T) {
	role4 := map[string]bool{"access:read": true, "alarm:read": true, "alarm:confirm": true}      // 保安
	role5 := map[string]bool{"workorder:read": true, "workorder:write": true, "parking:read": true, "parking:write": true} // 物业经理
	admin := map[string]bool{ // super_admin: 全部业务权限
		"access:read": true, "access:write": true, "alarm:read": true, "alarm:confirm": true, "alarm:write": true,
		"workorder:read": true, "workorder:write": true, "parking:read": true, "parking:write": true,
	}
	req := func(method, path, roleIDs string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set(ctxdata.CtxUserId, "1")
		r.Header.Set(ctxdata.CtxRoleIds, roleIDs)
		return r
	}
	call := func(granted map[string]bool, method, path, roleIDs string) int {
		rec := httptest.NewRecorder()
		Permission(&fakeUserClient{granted: granted}, nil)(downstream())(rec, req(method, path, roleIDs))
		return rec.Code
	}

	// 保安: 门禁查看/告警确认可, 工单/停车写被拒
	if c := call(role4, http.MethodGet, "/api/access/records", "4"); c != http.StatusOK {
		t.Errorf("保安 门禁查看: want 200, got %d", c)
	}
	if c := call(role4, http.MethodPut, "/api/alarm/:id/status", "4"); c != http.StatusOK {
		t.Errorf("保安 告警确认: want 200, got %d", c)
	}
	if c := call(role4, http.MethodPost, "/api/workorder", "4"); c != http.StatusForbidden {
		t.Errorf("保安 工单写: want 403, got %d", c)
	}
	if c := call(role4, http.MethodPost, "/api/parking/entry", "4"); c != http.StatusForbidden {
		t.Errorf("保安 停车写: want 403, got %d", c)
	}

	// 物业经理: 工单/停车可, 门禁/告警确认被拒
	if c := call(role5, http.MethodPost, "/api/workorder", "5"); c != http.StatusOK {
		t.Errorf("物业经理 工单写: want 200, got %d", c)
	}
	if c := call(role5, http.MethodPost, "/api/parking/entry", "5"); c != http.StatusOK {
		t.Errorf("物业经理 停车写: want 200, got %d", c)
	}
	if c := call(role5, http.MethodPut, "/api/alarm/:id/status", "5"); c != http.StatusForbidden {
		t.Errorf("物业经理 告警确认: want 403, got %d", c)
	}
	if c := call(role5, http.MethodGet, "/api/access/records", "5"); c != http.StatusForbidden {
		t.Errorf("物业经理 门禁查看: want 403, got %d", c)
	}

	// super_admin: 全部放行
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/workorder"},
		{http.MethodPost, "/api/parking/entry"},
		{http.MethodPut, "/api/alarm/:id/status"},
		{http.MethodGet, "/api/access/records"},
		{http.MethodPost, "/api/access/grant"},
	} {
		if c := call(admin, tc.method, tc.path, "1"); c != http.StatusOK {
			t.Errorf("super_admin %s %s: want 200, got %d", tc.method, tc.path, c)
		}
	}
}

// TestPermissionRedisCache 验证 Redis 缓存写入与命中短路(权限变更 TTL 内按缓存返回).
func TestPermissionRedisCache(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mini.Close()
	rdb := redisx.NewClient(&redisx.RedisConf{Addr: mini.Addr()})

	// 无权限(role=3 staff 仅 user:read)写用户 -> 403 且缓存 "0"
	denied := &fakeUserClient{granted: map[string]bool{}}
	r := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	r.Header.Set(ctxdata.CtxUserId, "9")
	r.Header.Set(ctxdata.CtxRoleIds, "3")
	rec := httptest.NewRecorder()
	Permission(denied, rdb)(downstream())(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied: want 403, got %d", rec.Code)
	}
	if v, _ := mini.Get("gw:perm:3:user:write"); v != "0" {
		t.Fatalf("cache denied = %q, want 0", v)
	}

	// 缓存命中: 即便翻转 granted, 同键仍返回 403(不回查 gRPC)
	denied.granted = map[string]bool{"user:write": true}
	rec2 := httptest.NewRecorder()
	Permission(denied, rdb)(downstream())(rec2, r)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("cache hit denied: want 403, got %d", rec2.Code)
	}
	if denied.callCount != 1 {
		t.Fatalf("cache should short-circuit gRPC, callCount=%d want 1", denied.callCount)
	}

	// 有权限(role=1 super_admin)查角色 -> 200 且缓存 "1"; 翻转后同键仍 200(命中缓存)
	allowed := &fakeUserClient{granted: map[string]bool{"role:read": true}}
	r3 := httptest.NewRequest(http.MethodGet, "/api/roles", nil)
	r3.Header.Set(ctxdata.CtxUserId, "1")
	r3.Header.Set(ctxdata.CtxRoleIds, "1")
	rec3 := httptest.NewRecorder()
	Permission(allowed, rdb)(downstream())(rec3, r3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("allowed: want 200, got %d", rec3.Code)
	}
	if v, _ := mini.Get("gw:perm:1:role:read"); v != "1" {
		t.Fatalf("cache allowed = %q, want 1", v)
	}
	allowed.granted = map[string]bool{}
	rec4 := httptest.NewRecorder()
	Permission(allowed, rdb)(downstream())(rec4, r3)
	if rec4.Code != http.StatusOK {
		t.Fatalf("cache hit allowed: want 200, got %d", rec4.Code)
	}
}
