package logic

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"onepark/app/user-manage/internal/config"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
)

// TestRBACCRUD 为 user-manage RBAC 五表 CRUD 的自测, 覆盖 10 个接口的成功/异常路径.
// 运行前提:
//  1. 已执行 deploy/sql/sys.sql 初始化五表(sys_user/sys_role/sys_menu/sys_user_role/sys_role_menu);
//  2. 设置环境变量 USER_MANAGE_TEST_DSN(如 root:pass@tcp(127.0.0.1:3306)/sys_db?charset=utf8mb4&parseTime=true).
//
// 未配置 DSN 时跳过, 保证无 DB 环境也能编译通过与 CI 绿灯.
func TestRBACCRUD(t *testing.T) {
	dsn := os.Getenv("USER_MANAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("USER_MANAGE_TEST_DSN 未设置, 跳过 RBAC CRUD 集成自测(需真实 MySQL + 已初始化 sys.sql)")
	}

	c := config.Config{}
	c.MySQL.DataSource = dsn
	ctx := svc.NewServiceContext(c)
	defer func() {
		if sqlDB, err := ctx.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	bg := context.Background()
	ts := time.Now().UnixNano()
	uname := fmt.Sprintf("ut_user_%d", ts)
	rkey := fmt.Sprintf("ut_role_%d", ts)
	mkey := fmt.Sprintf("ut_menu_%d", ts)

	// 1. UserCreate 成功
	uc := NewUserCreateLogic(bg, ctx)
	uresp, err := uc.UserCreate(&types.CreateUserReq{Username: uname, Password: "Secret@123", Nickname: "自测用户", Status: 1})
	if err != nil {
		t.Fatalf("[UserCreate] 成功路径失败: %v", err)
	}
	uid := uresp.Id
	if uid == 0 {
		t.Fatal("[UserCreate] 返回 Id 为空")
	}
	// 1b. UserCreate 重复用户名 -> 异常
	if _, err := uc.UserCreate(&types.CreateUserReq{Username: uname, Password: "x", Nickname: "dup"}); err == nil {
		t.Error("[UserCreate] 重复用户名应返回错误")
	}

	// 2. UserUpdate 成功
	if err := NewUserUpdateLogic(bg, ctx).UserUpdate(&types.UpdateUserReq{Id: uid, Nickname: "改名", Status: 0}); err != nil {
		t.Fatalf("[UserUpdate] 成功路径失败: %v", err)
	}

	// 3. UserDetail 成功
	d, err := NewUserDetailLogic(bg, ctx).UserDetail(&types.UserDetailReq{Id: uid})
	if err != nil {
		t.Fatalf("[UserDetail] 成功路径失败: %v", err)
	}
	if d.Nickname != "改名" {
		t.Errorf("[UserDetail] 昵称不符, got=%s want=改名", d.Nickname)
	}
	// 3b. UserDetail 不存在 -> 异常
	if _, err := NewUserDetailLogic(bg, ctx).UserDetail(&types.UserDetailReq{Id: 999999999}); err == nil {
		t.Error("[UserDetail] 查询不存在用户应返回错误")
	}

	// 4. UserList 成功
	lst, err := NewUserListLogic(bg, ctx).UserList(&types.UserListReq{})
	if err != nil {
		t.Fatalf("[UserList] 成功路径失败: %v", err)
	}
	if lst == nil {
		t.Fatal("[UserList] 返回 nil")
	}

	// 5. RoleCreate 成功
	rresp, err := NewRoleCreateLogic(bg, ctx).RoleCreate(&types.CreateRoleReq{RoleKey: rkey, RoleName: "自测角色", Remark: "ut"})
	if err != nil {
		t.Fatalf("[RoleCreate] 成功路径失败: %v", err)
	}
	rid := rresp.Id

	// 5b. RoleList 成功, 应包含刚创建的角色(第 11 个接口)
	rl, err := NewRoleListLogic(bg, ctx).RoleList(&types.RoleListReq{})
	if err != nil {
		t.Fatalf("[RoleList] 成功路径失败: %v", err)
	}
	roleFound := false
	for _, r := range rl.List {
		if r.Id == rid {
			roleFound = true
			break
		}
	}
	if !roleFound {
		t.Error("[RoleList] 列表中未包含刚创建的角色")
	}

	// 5c. UserCreate 带 role_id 应内联分配角色(一次提交 用户名+密码+角色)
	u2, err := NewUserCreateLogic(bg, ctx).UserCreate(&types.CreateUserReq{Username: uname + "_r", Password: "Secret@123", Nickname: "带角色", RoleId: rid})
	if err != nil {
		t.Fatalf("[UserCreate+role] 成功路径失败: %v", err)
	}
	rIDs, rerr := ctx.UserRoleModel.ListRoleIDs(bg, u2.Id)
	if rerr != nil {
		t.Fatalf("[UserCreate+role] 查询用户角色失败: %v", rerr)
	}
	hasRole := false
	for _, r := range rIDs {
		if r == rid {
			hasRole = true
			break
		}
	}
	if !hasRole {
		t.Error("[UserCreate+role] 未内联分配角色")
	}
	ctx.DB.Exec("DELETE FROM sys_user WHERE id = ?", u2.Id)
	// 5d. UserCreate 带不存在 role_id -> 异常
	if _, err := NewUserCreateLogic(bg, ctx).UserCreate(&types.CreateUserReq{Username: uname + "_x", Password: "x", RoleId: 999999999}); err == nil {
		t.Error("[UserCreate+role] 不存在角色应返回错误")
	}

	// 6. RoleAssign 成功
	if err := NewRoleAssignLogic(bg, ctx).RoleAssign(&types.AssignRoleReq{UserId: uid, RoleId: rid}); err != nil {
		t.Fatalf("[RoleAssign] 成功路径失败: %v", err)
	}
	// 6b. RoleAssign 不存在角色 -> 异常
	if err := NewRoleAssignLogic(bg, ctx).RoleAssign(&types.AssignRoleReq{UserId: uid, RoleId: 999999999}); err == nil {
		t.Error("[RoleAssign] 分配不存在角色应返回错误")
	}

	// 7. MenuCreate 成功
	mresp, err := NewMenuCreateLogic(bg, ctx).MenuCreate(&types.CreateMenuReq{ParentId: 0, MenuKey: mkey, MenuName: "自测菜单", Permission: "ut:read", Path: "/ut", Sort: 1})
	if err != nil {
		t.Fatalf("[MenuCreate] 成功路径失败: %v", err)
	}
	mid := mresp.Id

	// 8. RoleMenuAssign 成功
	if err := NewRoleMenuAssignLogic(bg, ctx).RoleMenuAssign(&types.AssignMenuReq{RoleId: rid, MenuId: mid}); err != nil {
		t.Fatalf("[RoleMenuAssign] 成功路径失败: %v", err)
	}

	// 9. PermissionCheck 已授权 -> Allowed=true
	pc, err := NewPermissionCheckLogic(bg, ctx).PermissionCheck(&types.CheckPermissionReq{UserId: uid, Permission: "ut:read"})
	if err != nil {
		t.Fatalf("[PermissionCheck] 成功路径失败: %v", err)
	}
	if !pc.Allowed {
		t.Error("[PermissionCheck] 已授权权限应 Allowed=true")
	}
	// 9b. PermissionCheck 未授权 -> Allowed=false
	pc2, err := NewPermissionCheckLogic(bg, ctx).PermissionCheck(&types.CheckPermissionReq{UserId: uid, Permission: "ut:never"})
	if err != nil {
		t.Fatalf("[PermissionCheck] 成功路径失败: %v", err)
	}
	if pc2.Allowed {
		t.Error("[PermissionCheck] 未授权权限应 Allowed=false")
	}

	// 10. UserDelete 成功(并清理角色/菜单, 保持库干净)
	if err := NewUserDeleteLogic(bg, ctx).UserDelete(&types.UserDeleteReq{Id: uid}); err != nil {
		t.Fatalf("[UserDelete] 成功路径失败: %v", err)
	}
	// 10b. UserDelete 不存在 -> 异常
	if err := NewUserDeleteLogic(bg, ctx).UserDelete(&types.UserDeleteReq{Id: 999999999}); err == nil {
		t.Error("[UserDelete] 删除不存在用户应返回错误")
	}
	// 清理本测试产生的角色/菜单(无对外删除接口, 直接清理)
	ctx.DB.Exec("DELETE FROM sys_role WHERE id = ?", rid)
	ctx.DB.Exec("DELETE FROM sys_menu WHERE id = ?", mid)
}
