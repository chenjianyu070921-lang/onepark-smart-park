package ctxdata

import "context"

// 全链路上下文键名, 网关层注入, 服务层透传.
const (
	CtxRequestId = "x-request-id"   // RequestId
	CtxUserId    = "x-user-id"      // 用户 ID
	CtxRoleIds   = "x-role-ids"     // 角色 ID 列表 (逗号分隔)
	CtxTenantId  = "x-tenant-id"    // 租户/园区 ID
)

func SetRequestId(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CtxRequestId, id)
}

func GetRequestId(ctx context.Context) string {
	if v, ok := ctx.Value(CtxRequestId).(string); ok {
		return v
	}
	return ""
}

func SetUserId(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, CtxUserId, id)
}

func GetUserId(ctx context.Context) int64 {
	if v, ok := ctx.Value(CtxUserId).(int64); ok {
		return v
	}
	return 0
}

// SetTenantId 向 context 写入租户/园区 ID，供 RBAC 数据隔离使用.
func SetTenantId(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, CtxTenantId, id)
}

// GetTenantId 从 context 读取租户/园区 ID，未注入时返回 0.
func GetTenantId(ctx context.Context) int64 {
	if v, ok := ctx.Value(CtxTenantId).(int64); ok {
		return v
	}
	return 0
}

// SetRoleIds 向 context 写入角色 ID 列表（逗号分隔字符串），供 RBAC 鉴权与行级 scope 使用.
func SetRoleIds(ctx context.Context, ids string) context.Context {
	return context.WithValue(ctx, CtxRoleIds, ids)
}

// GetRoleIds 从 context 读取角色 ID 列表，未注入时返回空字符串.
func GetRoleIds(ctx context.Context) string {
	if v, ok := ctx.Value(CtxRoleIds).(string); ok {
		return v
	}
	return ""
}

// CtxDataScope RBAC 数据范围(1全部 2本园区/租户 4本人), 由业务服务按角色写入, 供 datascope 使用.
const CtxDataScope = "x-data-scope"

// SetDataScope 向 context 写入数据范围级别.
func SetDataScope(ctx context.Context, scope int8) context.Context {
	return context.WithValue(ctx, CtxDataScope, scope)
}

// GetDataScope 从 context 读取数据范围级别, 未注入时返回 0(由 datascope 兜底为租户隔离).
func GetDataScope(ctx context.Context) int8 {
	if v, ok := ctx.Value(CtxDataScope).(int8); ok {
		return v
	}
	return 0
}
