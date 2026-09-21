package logic

import (
	"context"

	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/jwt"
	"onepark/common/tokenblk"

	"github.com/zeromicro/go-zero/core/logx"
)

type VerifyLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVerifyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VerifyLogic {
	return &VerifyLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Verify 纯 JWT 校验: 解析 access 令牌并返回身份载荷, 不查库、不要求调用方预置有效令牌.
// 与 Validate(需带令牌回显当前用户) 区分: 此处用于主动探测任意令牌有效性, 方便全班联调.
func (l *VerifyLogic) Verify(req *types.VerifyReq) (resp *types.VerifyResp, err error) {
	resp = &types.VerifyResp{Valid: false}
	if req.Token == "" {
		return resp, nil
	}
	claims, perr := jwt.Parse(l.svcCtx.JwtSecret, req.Token)
	if perr != nil || claims.Type != jwt.TypeAccess {
		return resp, nil
	}
	// 令牌吊销(注销)黑名单: Redis 不可用时降级为放行, 与 logout 的 Revoke 降级一致.
	// 吊销语义收口到本服务, 网关委托 Verify 后即统一生效(避免网关/服务端两处不一致).
	if l.svcCtx.Redis != nil && tokenblk.IsRevoked(l.ctx, l.svcCtx.Redis, claims.ID) {
		return resp, nil
	}
	resp.Valid = true
	resp.UserId = claims.UserId
	resp.RoleIds = claims.RoleIds
	resp.TenantId = claims.TenantId
	if claims.ExpiresAt != nil {
		resp.ExpiresAt = claims.ExpiresAt.Unix()
	}
	return resp, nil
}
