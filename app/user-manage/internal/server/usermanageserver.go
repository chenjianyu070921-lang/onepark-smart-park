package server

import (
	"context"

	"onepark/app/user-manage/internal/logic"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
	commonpb "onepark/proto/common"
	userpb "onepark/proto/user"
)

// UserManageServer 实现 userpb.UserManageServer, 复用现有 HTTP CRUD logic.
type UserManageServer struct {
	svcCtx *svc.ServiceContext
	userpb.UnimplementedUserManageServer
}

func NewUserManageServer(svcCtx *svc.ServiceContext) *UserManageServer {
	return &UserManageServer{svcCtx: svcCtx}
}

func (s *UserManageServer) Ping(ctx context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

func (s *UserManageServer) UserCreate(ctx context.Context, in *userpb.CreateUserReq) (*userpb.CreateUserResp, error) {
	resp, err := logic.NewUserCreateLogic(ctx, s.svcCtx).UserCreate(&types.CreateUserReq{
		Username: in.Username,
		Password: in.Password,
		Nickname: in.Nickname,
		Status:   int8(in.Status),
	})
	if err != nil {
		return nil, err
	}
	return &userpb.CreateUserResp{Id: resp.Id}, nil
}

func (s *UserManageServer) UserUpdate(ctx context.Context, in *userpb.UpdateUserReq) (*commonpb.Empty, error) {
	if err := logic.NewUserUpdateLogic(ctx, s.svcCtx).UserUpdate(&types.UpdateUserReq{
		Id:       in.Id,
		Nickname: in.Nickname,
		Status:   int8(in.Status),
	}); err != nil {
		return nil, err
	}
	return &commonpb.Empty{}, nil
}

func (s *UserManageServer) UserDelete(ctx context.Context, in *userpb.UserDeleteReq) (*commonpb.Empty, error) {
	if err := logic.NewUserDeleteLogic(ctx, s.svcCtx).UserDelete(&types.UserDeleteReq{Id: in.Id}); err != nil {
		return nil, err
	}
	return &commonpb.Empty{}, nil
}

func (s *UserManageServer) UserDetail(ctx context.Context, in *userpb.UserDetailReq) (*userpb.UserInfo, error) {
	u, err := logic.NewUserDetailLogic(ctx, s.svcCtx).UserDetail(&types.UserDetailReq{Id: in.Id})
	if err != nil {
		return nil, err
	}
	return &userpb.UserInfo{
		Id:        u.Id,
		Username:  u.Username,
		Nickname:  u.Nickname,
		Status:    int32(u.Status),
		CreatedAt: u.CreatedAt,
	}, nil
}

func (s *UserManageServer) UserList(ctx context.Context, _ *commonpb.Empty) (*userpb.UserListResp, error) {
	resp, err := logic.NewUserListLogic(ctx, s.svcCtx).UserList(&types.UserListReq{})
	if err != nil {
		return nil, err
	}
	infos := make([]*userpb.UserInfo, 0, len(resp.List))
	for _, u := range resp.List {
		infos = append(infos, &userpb.UserInfo{
			Id:        u.Id,
			Username:  u.Username,
			Nickname:  u.Nickname,
			Status:    int32(u.Status),
			CreatedAt: u.CreatedAt,
		})
	}
	return &userpb.UserListResp{List: infos, Total: resp.Total}, nil
}

// CheckPermission 网关统一 RBAC 入口: 委托 PermissionCheckLogic 校验用户是否拥有某 permission.
// 调用方为网关(已鉴权), 直接透传 in.UserId(网关注入的已鉴权用户ID), 不信任请求体之外的身份.
func (s *UserManageServer) CheckPermission(ctx context.Context, in *userpb.CheckPermissionReq) (*userpb.CheckPermissionResp, error) {
	resp, err := logic.NewPermissionCheckLogic(ctx, s.svcCtx).PermissionCheck(&types.CheckPermissionReq{
		UserId:    in.UserId,
		Permission: in.Permission,
	})
	if err != nil {
		return nil, err
	}
	return &userpb.CheckPermissionResp{Allowed: resp.Allowed}, nil
}
