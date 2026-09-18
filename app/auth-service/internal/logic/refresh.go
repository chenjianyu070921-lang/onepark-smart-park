package logic

import (
	"context"

	"onepark/app/auth-service/internal/model"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/jwt"

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

// Refresh 校验 refresh 令牌, 重新查询账号状态并签发新的 access + refresh 双令牌.
// 身份中心化: 每次刷新都校验账号是否被禁用/删除, 状态变更即时生效.
func (l *RefreshLogic) Refresh(req *types.RefreshReq) (resp *types.LoginResp, err error) {
	claims, perr := jwt.Parse(l.svcCtx.JwtSecret, req.RefreshToken)
	if perr != nil || claims.Type != jwt.TypeRefresh {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid refresh token")
	}

	user, derr := model.FindUserByID(l.ctx, l.svcCtx.DB, uint64(claims.UserId))
	if derr != nil {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid refresh token")
	}
	if user.Status != 1 {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "account disabled")
	}

	roleIds, rerr := model.ListRoleIDs(l.ctx, l.svcCtx.DB, user.ID)
	if rerr != nil {
		l.Errorf("查询用户角色失败: %v", rerr)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户角色失败")
	}
	roleStr := joinRoleIDs(roleIds)

	access, gerr := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, roleStr, 0, jwt.TypeAccess, l.svcCtx.JwtExpire)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}
	refresh, gerr := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, roleStr, 0, jwt.TypeRefresh, l.svcCtx.JwtRefresh)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}

	return &types.LoginResp{
		Token:        access,
		RefreshToken: refresh,
		Expire:       l.svcCtx.JwtExpire,
		UserId:       claims.UserId,
		RoleIds:      roleStr,
		TenantId:     0, // 平台级用户: sys_user 无租户维度, 待用户-租户映射落地后回填
	}, nil
}
