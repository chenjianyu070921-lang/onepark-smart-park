package server

import (
	"context"

	"onepark/app/auth-service/internal/logic"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	authpb "onepark/proto/auth"
	commonpb "onepark/proto/common"
)

// AuthServer 实现 authpb.AuthServiceServer, 复用现有 HTTP logic, 与网关 HTTP 入口共享同一套鉴权逻辑.
type AuthServer struct {
	svcCtx *svc.ServiceContext
	authpb.UnimplementedAuthServiceServer
}

func NewAuthServer(svcCtx *svc.ServiceContext) *AuthServer {
	return &AuthServer{svcCtx: svcCtx}
}

func (s *AuthServer) Ping(ctx context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

func (s *AuthServer) Login(ctx context.Context, in *authpb.LoginReq) (*authpb.LoginResp, error) {
	resp, err := logic.NewLoginLogic(ctx, s.svcCtx).Login(&types.LoginReq{
		Username: in.Username,
		Password: in.Password,
	})
	if err != nil {
		return nil, err
	}
	return &authpb.LoginResp{
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
		Expire:       resp.Expire,
		UserId:       resp.UserId,
		RoleIds:      resp.RoleIds,
		TenantId:     resp.TenantId,
	}, nil
}

func (s *AuthServer) Verify(ctx context.Context, in *authpb.VerifyReq) (*authpb.VerifyResp, error) {
	resp, err := logic.NewVerifyLogic(ctx, s.svcCtx).Verify(&types.VerifyReq{Token: in.Token})
	if err != nil {
		return nil, err
	}
	return &authpb.VerifyResp{
		Valid:     resp.Valid,
		UserId:    resp.UserId,
		RoleIds:   resp.RoleIds,
		TenantId:  resp.TenantId,
		ExpiresAt: resp.ExpiresAt,
	}, nil
}

func (s *AuthServer) Refresh(ctx context.Context, in *authpb.RefreshReq) (*authpb.LoginResp, error) {
	resp, err := logic.NewRefreshLogic(ctx, s.svcCtx).Refresh(&types.RefreshReq{RefreshToken: in.RefreshToken})
	if err != nil {
		return nil, err
	}
	return &authpb.LoginResp{
		Token:        resp.Token,
		RefreshToken: resp.RefreshToken,
		Expire:       resp.Expire,
		UserId:       resp.UserId,
		RoleIds:      resp.RoleIds,
		TenantId:     resp.TenantId,
	}, nil
}
