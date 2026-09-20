// assignworkorderlogic_test.go 派单逻辑单元测试 —— 第五阶段任务「补 workorder-service 单元测试: 派单」.
// 范围: 不依赖 DB 的派单前置约束(DB 事务/乐观锁由 m2-api-test.ps1 E2E 覆盖):
//   1. RBAC 派单权限口径: 仅系统管理员(1)/园区管理员(2)/物业客服(3)可派单;
//      维修(4)/业主(5)/保安(6)/停车管理员(7)及无角色均拒绝;
//   2. x-role-ids 解析容错: 空串/含空格/非法项跳过;
//   3. 派单 FSM 前置: 仅待派单(0)可 assign, 重复派单/跨状态派单必须被状态机拒绝.
package logic

import (
	"testing"

	"onepark/app/workorder-service/internal/state"
	"onepark/common/rbac"
)

// TestAssignPermission_RBAC 派单权限判定表驱动(与 assign logic 的 rbac.CanManageWorkOrder 前置一致).
func TestAssignPermission_RBAC(t *testing.T) {
	cases := []struct {
		name    string
		roleIds string
		allow   bool
	}{
		{"系统管理员可派单", "1", true},
		{"园区管理员可派单", "2", true},
		{"物业客服可派单", "3", true},
		{"多角色命中客服", "5,3", true},
		{"多角色仅维修+业主", "4,5", false},
		{"维修人员不可派单", "4", false},
		{"业主不可派单", "5", false},
		{"保安不可派单", "6", false},
		{"停车管理员不可派单", "7", false},
		{"无角色不可派单", "", false},
		{"非法角色不可派单", "9", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rbac.CanManageWorkOrder(rbac.ParseRoleIds(c.roleIds))
			if got != c.allow {
				t.Errorf("role-ids=%q 派单权限期望 %v, 实际 %v", c.roleIds, c.allow, got)
			}
		})
	}
}

// TestParseRoleIds_Tolerant x-role-ids 解析容错(派单接口网关注入链路的输入健壮性).
func TestParseRoleIds_Tolerant(t *testing.T) {
	if got := rbac.ParseRoleIds(""); got != nil {
		t.Errorf("空串期望 nil, 实际 %v", got)
	}
	if got := rbac.ParseRoleIds(" 3 , x , ,4"); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("带空格/非法项期望 [3 4], 实际 %v", got)
	}
}

// TestAssign_FSMGuard 派单流转前置: 仅待派单(0)可 assign;
// 重复派单(1→assign)与跨状态派单(2/3→assign)必须被状态机拒绝 —— 这是派单接口的 FSM 校验口径.
func TestAssign_FSMGuard(t *testing.T) {
	if _, ok := state.NextStatus(state.StatusPendingDispatch, state.ActionAssign); !ok {
		t.Fatal("待派单(0)执行 assign 应合法")
	}
	for _, s := range []int8{state.StatusProcessing, state.StatusPendingVerify, state.StatusCompleted, state.StatusClosed} {
		if _, ok := state.NextStatus(s, state.ActionAssign); ok {
			t.Errorf("状态 %d 执行 assign(重复/跨状态派单)应被拒绝", s)
		}
	}
}
