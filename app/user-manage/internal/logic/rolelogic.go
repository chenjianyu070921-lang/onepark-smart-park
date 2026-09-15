package logic

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"onepark/app/user-manage/internal/model"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ---- RoleCreate ----
type RoleCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRoleCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RoleCreateLogic {
	return &RoleCreateLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *RoleCreateLogic) RoleCreate(req *types.CreateRoleReq) (resp *types.CreateRoleResp, err error) {
	exist, qerr := l.svcCtx.RoleModel.FindByKey(l.ctx, req.RoleKey)
	if qerr != nil && !errors.Is(qerr, gorm.ErrRecordNotFound) {
		l.Errorf("查询角色失败: %v", qerr)
		return nil, errorx.NewError(errorx.ErrInternal, "查询角色失败")
	}
	if exist != nil {
		return nil, errorx.NewError(errorx.ErrRoleDuplicate, "角色标识已存在")
	}
	role := &model.SysRole{RoleKey: req.RoleKey, RoleName: req.RoleName, Remark: req.Remark}
	if ierr := l.svcCtx.RoleModel.Insert(l.ctx, role); ierr != nil {
		l.Errorf("写入角色失败: %v", ierr)
		return nil, errorx.NewError(errorx.ErrInternal, "写入角色失败")
	}
	return &types.CreateRoleResp{Id: role.ID}, nil
}

// ---- RoleAssign (用户-角色) ----
type RoleAssignLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRoleAssignLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RoleAssignLogic {
	return &RoleAssignLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *RoleAssignLogic) RoleAssign(req *types.AssignRoleReq) error {
	if _, err := l.svcCtx.UserModel.FindByID(l.ctx, req.UserId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrUserNotFound, "用户不存在")
		}
		return errorx.NewError(errorx.ErrInternal, "查询用户失败")
	}
	if _, err := l.svcCtx.RoleModel.FindByID(l.ctx, req.RoleId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrRoleNotFound, "角色不存在")
		}
		return errorx.NewError(errorx.ErrInternal, "查询角色失败")
	}
	// 幂等: 已分配则直接返回
	roleIDs, err := l.svcCtx.UserRoleModel.ListRoleIDs(l.ctx, req.UserId)
	if err != nil {
		return errorx.NewError(errorx.ErrInternal, "查询用户角色失败")
	}
	for _, rid := range roleIDs {
		if rid == req.RoleId {
			return nil
		}
	}
	if err := l.svcCtx.UserRoleModel.Assign(l.ctx, req.UserId, req.RoleId); err != nil {
		l.Errorf("分配角色失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "分配角色失败")
	}
	return nil
}
