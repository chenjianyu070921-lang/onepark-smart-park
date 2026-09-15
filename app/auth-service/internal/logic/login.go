package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"onepark/common/jwt"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type LoginLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLoginLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LoginLogic {
	return &LoginLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Login 校验用户名/密码(sha256), 签发 access + refresh 双令牌.
func (l *LoginLogic) Login(req *types.LoginReq) (resp *types.LoginResp, err error) {
	u, ok := l.svcCtx.Users[req.Username]
	if !ok || u.PwdHash == "" {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid username or password")
	}
	sum := sha256.Sum256([]byte(req.Password))
	if hex.EncodeToString(sum[:]) != u.PwdHash {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid username or password")
	}

	access, err := jwt.Generate(l.svcCtx.JwtSecret, u.UserId, u.RoleIds, u.TenantId, jwt.TypeAccess, l.svcCtx.JwtExpire)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrInternal, err.Error())
	}
	refresh, err := jwt.Generate(l.svcCtx.JwtSecret, u.UserId, u.RoleIds, u.TenantId, jwt.TypeRefresh, l.svcCtx.JwtRefresh)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrInternal, err.Error())
	}

	return &types.LoginResp{
		Token:        access,
		RefreshToken: refresh,
		Expire:       l.svcCtx.JwtExpire,
		UserId:       u.UserId,
		RoleIds:      u.RoleIds,
		TenantId:     u.TenantId,
	}, nil
}
