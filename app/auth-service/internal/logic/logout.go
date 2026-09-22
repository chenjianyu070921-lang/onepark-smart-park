package logic

import (
	"context"
	"time"

	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/jwt"
	"onepark/common/tokenblk"

	"github.com/zeromicro/go-zero/core/logx"
)

type LogoutLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLogoutLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LogoutLogic {
	return &LogoutLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// Logout 注销当前令牌: 将其 jti 加入 Redis 黑名单, TTL 为令牌剩余有效期.
// 已过期/非法的令牌无需吊销(本就无效), 直接返回成功.
func (l *LogoutLogic) Logout(req *types.LogoutReq) error {
	if req.Token == "" {
		return nil
	}
	claims, err := jwt.Parse(l.svcCtx.JwtSecret, req.Token)
	if err != nil {
		// 已过期或非法令牌, 无需吊销
		return nil
	}
	// 注销 refresh 令牌: 从 refresh 登记表吊销, 使其立即失效(真正结束会话).
	if claims.Type == jwt.TypeRefresh {
		if err := tokenblk.RevokeRefresh(l.ctx, l.svcCtx.Redis, claims.ID); err != nil {
			l.Errorf("注销 refresh 令牌失败: %v", err)
			return errorx.NewError(errorx.ErrInternal, "注销失败")
		}
		return nil
	}
	// 注销 access 令牌: 加入黑名单(原逻辑), TTL 为令牌剩余有效期.
	if claims.ExpiresAt == nil {
		return nil
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		return nil
	}
	if err := tokenblk.Revoke(l.ctx, l.svcCtx.Redis, claims.ID, ttl); err != nil {
		l.Errorf("注销令牌入黑名单失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "注销失败")
	}
	return nil
}
