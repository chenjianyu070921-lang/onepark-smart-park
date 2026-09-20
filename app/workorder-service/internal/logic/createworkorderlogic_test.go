// createworkorderlogic_test.go 创建工单逻辑单元测试 —— 第五阶段任务「补 workorder-service 单元测试: 创建」.
// 范围: 不依赖 DB 的纯函数与语义约束(DB 交互由 m2-api-test.ps1 E2E 覆盖).
//   1. buildCreateFlow 建单流水字段口径: from=-1 / to=待派单(0) / action=create / 操作人=发起人 / 租户与时间戳落位;
//   2. 建单初始状态语义: 建单即待派单(0), 与 FSM 流转表自洽(0 可 assign/close, 其余动作拒绝).
package logic

import (
	"testing"
	"time"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
)

// TestBuildCreateFlow 校验建单流水(审计记录)的每一项字段口径.
// 流水是"主表+流水同事务"审计闭环的一半, 字段错会导致审计口径与状态机不一致.
func TestBuildCreateFlow(t *testing.T) {
	tenantID, reporterID := int64(1), int64(3001)
	now := time.Unix(1758200000, 0)
	wo := &model.WorkOrder{}
	wo.ID = 42 // 主键位于内嵌 gormx.BaseModel; OrderNo/TenantID 等由调用方落位, 流水只关心审计维度.

	flow := buildCreateFlow(tenantID, reporterID, wo, now)

	if flow.WorkOrderID != 42 {
		t.Errorf("流水工单ID期望 42, 实际 %d", flow.WorkOrderID)
	}
	if flow.TenantID != tenantID {
		t.Errorf("流水租户ID期望 %d, 实际 %d", tenantID, flow.TenantID)
	}
	if flow.FromStatus != -1 {
		t.Errorf("建单流水 from 期望 -1(无来源), 实际 %d", flow.FromStatus)
	}
	if flow.ToStatus != state.StatusPendingDispatch {
		t.Errorf("建单流水 to 期望 待派单(%d), 实际 %d", state.StatusPendingDispatch, flow.ToStatus)
	}
	if flow.Action != state.ActionCreate {
		t.Errorf("建单流水 action 期望 %s, 实际 %s", state.ActionCreate, flow.Action)
	}
	if flow.OperatorID != reporterID {
		t.Errorf("建单流水操作人期望发起人 %d, 实际 %d", reporterID, flow.OperatorID)
	}
	if !flow.CreatedAt.Equal(now) || !flow.UpdatedAt.Equal(now) {
		t.Errorf("流水时间戳期望 %v, 实际 created=%v updated=%v", now, flow.CreatedAt, flow.UpdatedAt)
	}
}

// TestCreateInitialStatus_ChecksWithFSM 建单即待派单(0)的初始状态必须与流转表自洽:
// 该状态下仅允许 assign(派单)与 close(取消=关闭), 其余动作全部拒绝.
func TestCreateInitialStatus_ChecksWithFSM(t *testing.T) {
	s := state.StatusPendingDispatch

	if !state.CanTransition(s, state.ActionAssign) {
		t.Error("建单后必须可直接派单(0 --assign--> 1)")
	}
	if !state.CanTransition(s, state.ActionClose) {
		t.Error("建单后必须可关闭(取消场景: 0 --close--> 4)")
	}
	for _, act := range []string{state.ActionSubmit, state.ActionApprove, state.ActionReject} {
		if state.CanTransition(s, act) {
			t.Errorf("待派单状态执行 %s 应被拒绝", act)
		}
	}
}
