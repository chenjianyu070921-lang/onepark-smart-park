// Package rpcserver 实现 access-control gRPC 服务(门禁控制面, 端口 9010).
//
// 只暴露两个动作: CheckPermission(只读判定) 与 RemoteOpen(开门, 不可逆)。
// 二者都**复用 internal/logic 的同一份实现**, 不在 gRPC 侧重写判定:
// 门禁的两条入口(HTTP #47 / gRPC)一旦各写一套, 必然在某天漂移成两种口径 ——
// 一边放行另一边拒绝 —— 而开门是不可逆的物理动作, 没有"重试修正"的机会。
package rpcserver

import (
	"context"
	"strings"
	"time"

	"onepark/app/access-control-service/internal/logic"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	accesspb "onepark/proto/access"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccessServer 门禁 gRPC 服务实现.
type AccessServer struct {
	accesspb.UnimplementedAccessControlServiceServer
	svcCtx *svc.ServiceContext
}

// NewAccessServer 构造门禁 gRPC 服务.
func NewAccessServer(svcCtx *svc.ServiceContext) *AccessServer {
	return &AccessServer{svcCtx: svcCtx}
}

// Ping 探活.
func (s *AccessServer) Ping(_ context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// CheckPermission 判定某人在指定时刻能否通过某扇门.
//
// 失败即拒绝(fail-closed): 存储不可用时返回 Unavailable 而不是 allowed=true,
// 因为"查不到授权"与"没权限"在门禁场景必须同归为不放行。
func (s *AccessServer) CheckPermission(ctx context.Context, req *accesspb.CheckPermissionReq) (*accesspb.CheckPermissionResp, error) {
	if req.GetTenantId() == 0 || req.GetPersonId() == 0 || strings.TrimSpace(req.GetDeviceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id / person_id / device_id 均必填")
	}
	if s.svcCtx.Permissions == nil {
		return nil, status.Error(codes.Unavailable, "权限存储未就绪(MySQL 未配置)")
	}

	p, err := s.svcCtx.Permissions.FindEffective(ctx, req.GetTenantId(), req.GetPersonId(), req.GetDeviceId())
	if err != nil {
		logx.WithContext(ctx).Errorf("access rpc find permission failed person=%d device=%s: %v",
			req.GetPersonId(), req.GetDeviceId(), err)
		return nil, status.Error(codes.Internal, "查询授权失败")
	}

	// at 可注入是为了让"时间段权限"可被确定性验证; 缺省取当前时刻。
	at := time.Now()
	if req.GetAt() > 0 {
		at = time.Unix(req.GetAt(), 0)
	}
	allowed, reason := logic.PermissionAllowed(p, at)
	resp := &accesspb.CheckPermissionResp{Allowed: allowed, Reason: reason}
	if p != nil && p.ExpireAt != nil {
		resp.ExpireAt = p.ExpireAt.Unix()
	}
	return resp, nil
}

// RemoteOpen 远程开门.
//
// 与 HTTP #47 共用 logic.RemoteOpenLogic: 幂等键、白名单/授权判定、审计落库全部一致。
// gRPC 侧没有网关注入的租户与操作人, 由调用方在请求里显式透传 —— 缺失时 logic 会按
// 缺少参数拒绝(不会退化成"匿名开门")。
func (s *AccessServer) RemoteOpen(ctx context.Context, req *accesspb.RemoteOpenReq) (*accesspb.RemoteOpenResp, error) {
	c := ctxdata.SetTenantId(ctx, req.GetTenantId())
	c = ctxdata.SetUserId(c, req.GetOperatorId())

	resp, err := logic.NewRemoteOpenLogic(c, s.svcCtx).RemoteOpen(&types.RemoteOpenReq{
		DeviceId:  req.GetDeviceId(),
		Reason:    req.GetReason(),
		RequestId: req.GetRequestId(),
	})
	if err != nil {
		// 原样上抛: 业务错误码由 logic 决定, 此处不做二次包装以免掩盖原因.
		return nil, err
	}
	return &accesspb.RemoteOpenResp{
		Success:   resp.Success,
		DeviceId:  resp.DeviceId,
		Message:   resp.Message,
		RequestId: strings.TrimSpace(req.GetRequestId()),
	}, nil
}
