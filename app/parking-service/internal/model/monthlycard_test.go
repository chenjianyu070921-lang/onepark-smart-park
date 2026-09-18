// monthlycard_test.go 停车月卡单元测试 —— 第五阶段任务「停车卡功能完善(收尾)」.
// 范围: 不依赖 DB 的判定口径与降级行为(DB 查询路径由 m2-api-test.ps1 E2E 的月卡链路覆盖):
//   1. HasActiveMonthlyCard: DB 未初始化(nil)必须降级返回 false, 不阻断入场;
//   2. 状态码与表名契约: 生效/停用枚举、表名 monthly_card 与 DDL 对齐, 防止误改.
package model

import (
	"testing"
	"time"
)

// TestHasActiveMonthlyCard_Degrade DB 未初始化时的降级口径:
// 返回 false(按临时车计费), 绝不 panic —— 与 ResolveVehicleType 的 DB nil 降级链路配套.
func TestHasActiveMonthlyCard_Degrade(t *testing.T) {
	if HasActiveMonthlyCard(nil, 1, "苏A12345", time.Now()) {
		t.Error("DB 为 nil 时应降级返回 false(按临时车处理)")
	}
}

// TestMonthlyCard_Contract 月卡模型契约: 表名与状态枚举冻结(识别 SQL/DDL 均依赖这些字面量).
func TestMonthlyCard_Contract(t *testing.T) {
	if (MonthlyCard{}).TableName() != "monthly_card" {
		t.Errorf("月卡表名期望 monthly_card, 实际 %s", (MonthlyCard{}).TableName())
	}
	if MonthlyCardStatusActive != 1 || MonthlyCardStatusDisabled != 2 {
		t.Errorf("月卡状态枚举期望 生效=1/停用=2, 实际 %d/%d", MonthlyCardStatusActive, MonthlyCardStatusDisabled)
	}
}
