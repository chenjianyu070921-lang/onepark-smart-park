package lease

import (
	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/types"
)

// dateLayout 合同日期对外格式.
const dateLayout = "2006-01-02"

// toContractDTO 将数据模型转换为对外契约对象.
// 金额经 decimal.String() 输出为字符串, 避免 JSON 浮点精度丢失.
func toContractDTO(m *model.LeaseContract) types.Contract {
	return types.Contract{
		Id:          m.Id,
		ContractNo:  m.ContractNo,
		TenantId:    m.TenantId,
		TenantName:  m.TenantName,
		ZoneCode:    m.ZoneCode,
		AreaSqm:     m.AreaSqm,
		MonthlyRent: m.MonthlyRent.String(),
		Deposit:     m.Deposit.String(),
		StartDate:   m.StartDate.Format(dateLayout),
		EndDate:     m.EndDate.Format(dateLayout),
		Status:      int32(m.Status),
		CreatedAt:   m.CreatedAt.Unix(),
		UpdatedAt:   m.UpdatedAt.Unix(),
	}
}
