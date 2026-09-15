package logic

import (
	"context"

	"onepark/common/jwt"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type RefreshLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRefreshLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RefreshLogic {
	return &RefreshLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Refresh 校验 refresh 令牌, 签发新的 access + refresh 双令牌.
func (l *RefreshLogic) Refresh(req *types.RefreshReq) (resp *types.LoginResp, err error) {
	claims, err := jwt.Parse(l.svcCtx.JwtSecret, req.RefreshToken)
	if err != nil || claims.Type != jwt.TypeRefresh {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid refresh token")
	}

	access, err := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, claims.RoleIds, claims.TenantId, jwt.TypeAccess, l.svcCtx.JwtExpire)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrInternal, err.Error())
	}
	refresh, err := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, claims.RoleIds, claims.TenantId, jwt.TypeRefresh, l.svcCtx.JwtRefresh)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrInternal, err.Error())
	}

	return &types.LoginResp{
		Token:        access,
		RefreshToken: refresh,
		Expire:       l.svcCtx.JwtExpire,
		UserId:       claims.UserId,
		RoleIds:      claims.RoleIds,
		TenantId:     claims.TenantId,
	}, nil
}
