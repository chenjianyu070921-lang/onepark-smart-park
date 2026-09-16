// Package datascope 提供 RBAC 数据权限(按租户/园区隔离)的统一 gorm 作用域.
// 业务服务在查询处显式 .Scopes(datascope.FromCtx(ctx)) 即可, 替代手写 .Where("tenant_id=?", ...),
// 避免散落遗漏, 并支持 全部/本园区(tenant_id)/本人(create_by) 三级数据范围.
// 设计为 opt-in: 仅对业务数据表调用, 全局配置表(sys_menu/sys_role 等)不调用, 防止误隔离.
package datascope

import (
	"context"

	"gorm.io/gorm"
	"onepark/common/ctxdata"
)

// 数据范围级别.
const (
	ScopeAll    int8 = 1 // 全部数据(超管)
	ScopeTenant int8 = 2 // 本园区/租户: tenant_id = ?
	ScopeSelf   int8 = 4 // 本人: create_by = user_id
)

// FromCtx 依据 ctxdata 中的角色 data_scope + 当前用户身份, 构造 gorm 查询作用域.
// data_scope 未显式设置时默认 ScopeTenant(最常用, 安全兜底).
func FromCtx(ctx context.Context) func(*gorm.DB) *gorm.DB {
	q, args := whereOf(ctx)
	return func(db *gorm.DB) *gorm.DB {
		if q == "" {
			return db
		}
		return db.Where(q, args...)
	}
}

// whereOf 计算数据权限 WHERE 片段与参数(纯函数, 便于单测, 不依赖 gorm 连接).
func whereOf(ctx context.Context) (string, []any) {
	switch ctxdata.GetDataScope(ctx) {
	case ScopeAll:
		return "", nil
	case ScopeSelf:
		return "create_by = ?", []any{ctxdata.GetUserId(ctx)}
	case ScopeTenant, 0:
		return "tenant_id = ?", []any{ctxdata.GetTenantId(ctx)}
	default:
		return "tenant_id = ?", []any{ctxdata.GetTenantId(ctx)}
	}
}
