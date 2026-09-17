package logic

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"onepark/app/user-manage/internal/model"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ---- UserCreate ----
type UserCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserCreateLogic {
	return &UserCreateLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UserCreateLogic) UserCreate(req *types.CreateUserReq) (resp *types.CreateUserResp, err error) {
	exist, qerr := l.svcCtx.UserModel.FindByUsername(l.ctx, req.Username)
	if qerr != nil && !errors.Is(qerr, gorm.ErrRecordNotFound) {
		l.Errorf("查询用户失败: %v", qerr)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户失败")
	}
	if exist != nil {
		return nil, errorx.NewError(errorx.ErrUserDuplicate, "用户名已存在")
	}

	hashed, berr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if berr != nil {
		l.Errorf("密码加密失败: %v", berr)
		return nil, errorx.NewError(errorx.ErrInternal, "密码加密失败")
	}

	status := req.Status
	if status == 0 {
		status = 1
	}
	user := &model.SysUser{
		Username: req.Username,
		Password: string(hashed),
		Nickname: req.Nickname,
		Status:   status,
	}
	if ierr := l.svcCtx.UserModel.Insert(l.ctx, user); ierr != nil {
		l.Errorf("写入用户失败: %v", ierr)
		return nil, errorx.NewError(errorx.ErrInternal, "写入用户失败")
	}
	// 可选: 创建时内联分配角色(场景: 管理员一次提交 用户名+密码+角色)
	if req.RoleId != 0 {
		if _, rerr := l.svcCtx.RoleModel.FindByID(l.ctx, req.RoleId); rerr != nil {
			if errors.Is(rerr, gorm.ErrRecordNotFound) {
				return nil, errorx.NewError(errorx.ErrRoleNotFound, "角色不存在")
			}
			l.Errorf("查询角色失败: %v", rerr)
			return nil, errorx.NewError(errorx.ErrInternal, "查询角色失败")
		}
		if aerr := l.svcCtx.UserRoleModel.Assign(l.ctx, user.ID, req.RoleId); aerr != nil {
			l.Errorf("分配角色失败: %v", aerr)
			return nil, errorx.NewError(errorx.ErrInternal, "分配角色失败")
		}
	}
	return &types.CreateUserResp{Id: user.ID}, nil
}

// ---- UserUpdate ----
type UserUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserUpdateLogic {
	return &UserUpdateLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UserUpdateLogic) UserUpdate(req *types.UpdateUserReq) error {
	user, err := l.svcCtx.UserModel.FindByID(l.ctx, req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrUserNotFound, "用户不存在")
		}
		l.Errorf("查询用户失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "查询用户失败")
	}
	user.Nickname = req.Nickname
	user.Status = req.Status
	if err := l.svcCtx.UserModel.Update(l.ctx, user); err != nil {
		l.Errorf("更新用户失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "更新用户失败")
	}
	return nil
}

// ---- UserDelete ----
type UserDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserDeleteLogic {
	return &UserDeleteLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UserDeleteLogic) UserDelete(req *types.UserDeleteReq) error {
	if _, err := l.svcCtx.UserModel.FindByID(l.ctx, req.Id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrUserNotFound, "用户不存在")
		}
		l.Errorf("查询用户失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "查询用户失败")
	}
	if err := l.svcCtx.UserModel.Delete(l.ctx, req.Id); err != nil {
		l.Errorf("删除用户失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "删除用户失败")
	}
	return nil
}

// ---- UserDetail ----
type UserDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserDetailLogic {
	return &UserDetailLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UserDetailLogic) UserDetail(req *types.UserDetailReq) (resp *types.UserInfo, err error) {
	user, err := l.svcCtx.UserModel.FindByID(l.ctx, req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrUserNotFound, "用户不存在")
		}
		l.Errorf("查询用户失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户失败")
	}
	return &types.UserInfo{
		Id:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		Status:    user.Status,
		CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

// ---- UserList ----
type UserListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUserListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UserListLogic {
	return &UserListLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UserListLogic) UserList(req *types.UserListReq) (resp *types.UserListResp, err error) {
	const page, size = 1, 20
	list, total, err := l.svcCtx.UserModel.FindList(l.ctx, page, size, "")
	if err != nil {
		l.Errorf("查询用户列表失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户列表失败")
	}
	infos := make([]types.UserInfo, 0, len(list))
	for _, u := range list {
		infos = append(infos, types.UserInfo{
			Id:        u.ID,
			Username:  u.Username,
			Nickname:  u.Nickname,
			Status:    u.Status,
			CreatedAt: u.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return &types.UserListResp{List: infos, Total: total}, nil
}
