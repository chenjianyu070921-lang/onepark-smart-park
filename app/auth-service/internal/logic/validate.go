package logic

import (
	"context"

	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type ValidateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewValidateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ValidateLogic {
	return &ValidateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Validate 读取 JWT 中间件注入 ctxdata 的用户身份并返回.
// 中间件已拦截无/无效令牌(401), 此处仅依赖注入的上下文.
func (l *ValidateLogic) Validate(req *types.ValidateReq) (resp *types.ValidateResp, err error) {
	userId := ctxdata.GetUserId(l.ctx)
	if userId == 0 {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "unauthorized")
	}
	return &types.ValidateResp{
		UserId:   userId,
		RoleIds:  ctxdata.GetRoleIds(l.ctx),
		TenantId: ctxdata.GetTenantId(l.ctx),
	}, nil
}
