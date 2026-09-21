package logic

import (
	"context"
	"time"

	"onepark/app/auth-service/internal/model"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/jwt"
	"onepark/common/tokenblk"

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
	// refresh 令牌必须仍在 Redis 登记表中(未被注销/轮换吊销); 不在则视为已失效.
	if !tokenblk.RefreshExists(l.ctx, l.svcCtx.Redis, claims.ID) {
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

	access, gerr := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, roleStr, user.TenantId, jwt.TypeAccess, l.svcCtx.JwtExpire)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}
	refresh, gerr := jwt.Generate(l.svcCtx.JwtSecret, claims.UserId, roleStr, user.TenantId, jwt.TypeRefresh, l.svcCtx.JwtRefresh)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}
	// 刷新令牌轮换: 吊销旧 refresh jti, 登记新 refresh jti(防 7d 窗口内令牌重用). Redis 不可用时降级为无操作.
	if nc, nerr := jwt.Parse(l.svcCtx.JwtSecret, refresh); nerr == nil {
		_ = tokenblk.RevokeRefresh(l.ctx, l.svcCtx.Redis, claims.ID)
		_ = tokenblk.StoreRefresh(l.ctx, l.svcCtx.Redis, nc.ID, time.Duration(l.svcCtx.JwtRefresh)*time.Second)
	}

	return &types.LoginResp{
		Token:        access,
		RefreshToken: refresh,
		Expire:       l.svcCtx.JwtExpire,
		UserId:       claims.UserId,
		RoleIds:      roleStr,
		TenantId:     user.TenantId, // 回填用户真实所属园区(tenant_id 已由 M6 多租户试点迁移加入 sys_user 并回填)
	}, nil
}
