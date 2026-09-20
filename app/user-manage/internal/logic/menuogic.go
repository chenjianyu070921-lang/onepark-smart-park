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

// ---- MenuCreate ----
type MenuCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMenuCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MenuCreateLogic {
	return &MenuCreateLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *MenuCreateLogic) MenuCreate(req *types.CreateMenuReq) (resp *types.CreateMenuResp, err error) {
	exist, qerr := l.svcCtx.MenuModel.FindByKey(l.ctx, req.MenuKey)
	if qerr != nil && !errors.Is(qerr, gorm.ErrRecordNotFound) {
		l.Errorf("查询菜单失败: %v", qerr)
		return nil, errorx.NewError(errorx.ErrInternal, "查询菜单失败")
	}
	if exist != nil {
		return nil, errorx.NewError(errorx.ErrMenuDuplicate, "菜单标识已存在")
	}
	menu := &model.SysMenu{
		ParentID:   req.ParentId,
		MenuKey:    req.MenuKey,
		MenuName:   req.MenuName,
		Permission: req.Permission,
		Path:       req.Path,
		Sort:       req.Sort,
	}
	if ierr := l.svcCtx.MenuModel.Insert(l.ctx, menu); ierr != nil {
		l.Errorf("写入菜单失败: %v", ierr)
		return nil, errorx.NewError(errorx.ErrInternal, "写入菜单失败")
	}
	return &types.CreateMenuResp{Id: menu.ID}, nil
}

// ---- RoleMenuAssign (角色-菜单/权限) ----
type RoleMenuAssignLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRoleMenuAssignLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RoleMenuAssignLogic {
	return &RoleMenuAssignLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *RoleMenuAssignLogic) RoleMenuAssign(req *types.AssignMenuReq) error {
	if _, err := l.svcCtx.RoleModel.FindByID(l.ctx, req.RoleId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrRoleNotFound, "角色不存在")
		}
		return errorx.NewError(errorx.ErrInternal, "查询角色失败")
	}
	if _, err := l.svcCtx.MenuModel.FindByID(l.ctx, req.MenuId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrMenuNotFound, "菜单不存在")
		}
		return errorx.NewError(errorx.ErrInternal, "查询菜单失败")
	}
	// 幂等: 已分配则直接返回
	menuIDs, err := l.svcCtx.RoleMenuModel.ListMenuIDs(l.ctx, req.RoleId)
	if err != nil {
		return errorx.NewError(errorx.ErrInternal, "查询角色菜单失败")
	}
	for _, mid := range menuIDs {
		if mid == req.MenuId {
			return nil
		}
	}
	if err := l.svcCtx.RoleMenuModel.Assign(l.ctx, req.RoleId, req.MenuId); err != nil {
		l.Errorf("分配菜单权限失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "分配菜单权限失败")
	}
	return nil
}
