package lease

import (
	"github.com/shopspring/decimal"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/types"
)

// dateLayout 合同日期对外格式.
const dateLayout = "2006-01-02"

// moneyScale 金额对外显示的小数位数, 与 DDL 声明保持一致
// (lease_contract.monthly_rent / deposit 与 lease_bill.amount 均为 DECIMAL(12,2)).
const moneyScale int32 = 2

// moneyString 把金额渲染为**定长小数**字符串.
//
// 为什么不用 decimal.String(): 它会丢掉末尾的零 —— DB 里 DECIMAL(12,2) 存的是 10000.50,
// 读回来 String() 得到 "10000.5", 前端就显示成"月租 ¥10000.5"。
// 数值虽相同, 但金额的规范表示应与列声明的精度一致(这也是前端不补零也能显示正确的保证)。
func moneyString(d decimal.Decimal) string {
	return d.StringFixed(moneyScale)
}

// renewNoticeMaxDays 续签提醒窗口上限, 防止配成 10000 天导致"全部合同都在提醒".
const renewNoticeMaxDays int64 = 365

// toContractDTO 将数据模型转换为对外契约对象.
// 金额以字符串输出(避免 JSON 浮点精度丢失), 并按列声明的精度补零, 见 moneyString.
func toContractDTO(m *model.LeaseContract) types.Contract {
	return types.Contract{
		Id:              m.Id,
		ContractNo:      m.ContractNo,
		TenantId:        m.TenantId,
		TenantName:      m.TenantName,
		ZoneCode:        m.ZoneCode,
		AreaSqm:         m.AreaSqm,
		MonthlyRent:     moneyString(m.MonthlyRent),
		Deposit:         moneyString(m.Deposit),
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
