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
