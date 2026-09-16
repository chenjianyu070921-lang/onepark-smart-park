package lease

import (
	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/types"
)

// dateLayout 合同日期对外格式.
const dateLayout = "2006-01-02"

// renewNoticeMaxDays 续签提醒窗口上限, 防止配成 10000 天导致"全部合同都在提醒".
const renewNoticeMaxDays int64 = 365

// toContractDTO 将数据模型转换为对外契约对象.
// 金额经 decimal.String() 输出为字符串, 避免 JSON 浮点精度丢失.
func toContractDTO(m *model.LeaseContract) types.Contract {
	return types.Contract{
		Id:              m.Id,
		ContractNo:      m.ContractNo,
		TenantId:        m.TenantId,
		TenantName:      m.TenantName,
		ZoneCode:        m.ZoneCode,
		AreaSqm:         m.AreaSqm,
		MonthlyRent:     m.MonthlyRent.String(),
		Deposit:         m.Deposit.String(),
		StartDate:       m.StartDate.Format(dateLayout),
		EndDate:         m.EndDate.Format(dateLayout),
		Status:          int32(m.Status),
		AutoRenew:       int32(m.AutoRenew),
		RenewNoticeDays: m.RenewNoticeDays,
		CreatedAt:       m.CreatedAt.Unix(),
		UpdatedAt:       m.UpdatedAt.Unix(),
	}
}

// validAutoRenew 校验自动续约开关: 仅允许 0(到期即止) / 1(自动续约).
func validAutoRenew(v int32) bool {
	return v == int32(model.AutoRenewOff) || v == int32(model.AutoRenewOn)
}

// validNoticeDays 校验续签提醒窗口天数.
func validNoticeDays(v int64) bool {
	return v >= 0 && v <= renewNoticeMaxDays
}
