package logic

import (
	"context"

	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// tenantOf 取网关注入的租户ID, 缺失(<=0)直接拒绝.
//
// 为什么不能"取不到就当 0/1": 租户是账单/规则的行级隔离维度,
// 静默兜底会让 A 园区看到 B 园区的账单, 属于数据越权; 所以宁可报错让调用方修网关.
func tenantOf(ctx context.Context) (uint64, error) {
	id := ctxdata.GetTenantId(ctx)
	if id <= 0 {
		return 0, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	return uint64(id), nil
}
