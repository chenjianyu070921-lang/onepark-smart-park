package logic

import (
	"context"

	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// RevokeAccessLogic 门禁撤权逻辑(docs/m3/04 #46): 删除人员×设备对应的权限记录.
type RevokeAccessLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRevokeAccessLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RevokeAccessLogic {
	return &RevokeAccessLogic{ctx: ctx, svcCtx: svcCtx}
}

// RevokeAccess 按人员与设备列表删除权限(绑定租户, 防止跨园区误删).
// 完全无匹配时返回错误, 让调用方知道"撤了个不存在的权限", 而不是悄悄返回 0.
func (l *RevokeAccessLogic) RevokeAccess(req *types.RevokeAccessReq) (*types.RevokeAccessResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Permissions == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "权限存储未就绪(MySQL 未配置)")
	}

	personIDs, deviceIDs, err := normalizeTargets(req.PersonIds, req.DeviceIds)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, err.Error())
	}

	revoked, err := l.svcCtx.Permissions.Revoke(l.ctx, tenantID, personIDs, deviceIDs)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessRevoke, "撤销门禁权限失败")
	}
	if revoked == 0 {
		return nil, errorx.NewError(errorx.ErrAccessRevoke, "权限记录不存在")
	}
	return &types.RevokeAccessResp{Revoked: revoked}, nil
}
