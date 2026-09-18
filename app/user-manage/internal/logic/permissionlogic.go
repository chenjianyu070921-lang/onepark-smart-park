package logic

import (
	"context"

	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ---- PermissionCheck ----
type PermissionCheckLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPermissionCheckLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PermissionCheckLogic {
	return &PermissionCheckLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// PermissionCheck 校验用户是否拥有某 permission:
// user -> user_role -> role_menu -> menu.permission(去重汇总).
// 以网关注入的登录身份为准(修复越权: 不再信任请求体 user_id); 无身份的内部调用回退到 req.UserId.
func (l *PermissionCheckLogic) PermissionCheck(req *types.CheckPermissionReq) (resp *types.CheckPermissionResp, err error) {
	userID := uint64(ctxdata.GetUserId(l.ctx))
	if userID == 0 {
		userID = req.UserId
	}
	roleIDs, err := l.svcCtx.UserRoleModel.ListRoleIDs(l.ctx, userID)
	if err != nil {
		l.Errorf("查询用户角色失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户角色失败")
	}
	if len(roleIDs) == 0 {
		return &types.CheckPermissionResp{Allowed: false}, nil
	}
	perms, err := l.svcCtx.RoleMenuModel.ListPermissions(l.ctx, roleIDs)
	if err != nil {
		l.Errorf("查询权限失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询权限失败")
	}
	allowed := false
	for _, p := range perms {
		if p == req.Permission {
			allowed = true
			break
		}
	}
	return &types.CheckPermissionResp{Allowed: allowed}, nil
}
