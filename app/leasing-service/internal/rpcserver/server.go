// Package rpcserver 实现 leasing gRPC 服务
// (组长一周开发任务计划 周六「leasing-service gRPC 业务接口补充」)。
// 端口规划 9051; 提供 Ping 探活与 GetContract / GetOccupancy 两个业务接口。
//
// 定位: **薄适配层** —— 把 gRPC 请求转成已有 HTTP logic 的入参, 再把 DTO 映射为 proto。
// 刻意不在本层重写查询: 同一个"查合同"若实现两遍, HTTP 与 gRPC 的返回迟早会不一致,
// 而调用方无从判断该信哪个。复用 logic 就是让两个协议共用唯一的事实来源。
package rpcserver

import (
	"context"

	leasingpb "onepark/proto/leasing"
	commonpb "onepark/proto/common"
	"onepark/app/leasing-service/internal/logic/lease"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// LeasingServer 招商租赁 gRPC 服务实现.
type LeasingServer struct {
	leasingpb.UnimplementedLeasingServiceServer
	svcCtx *svc.ServiceContext
}

// NewLeasingServer 构造 gRPC 服务.
func NewLeasingServer(svcCtx *svc.ServiceContext) *LeasingServer {
	return &LeasingServer{svcCtx: svcCtx}
}

// Ping 探活.
func (s *LeasingServer) Ping(_ context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// GetContract 按主键查询合同主信息.
func (s *LeasingServer) GetContract(ctx context.Context, req *leasingpb.GetContractReq) (*leasingpb.GetContractResp, error) {
	if req.GetId() <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "合同ID不能为空")
	}

	// 复用 HTTP 详情逻辑: 两个协议的数据来源与口径完全一致
	detail, err := lease.NewContractDetailLogic(ctx, s.svcCtx).
		ContractDetail(&types.ContractDetailReq{Id: req.GetId()})
	if err != nil {
		return nil, err
	}

	// 租户隔离必须在这里显式做: HTTP 侧靠网关注入的租户上下文约束,
	// 而 gRPC 调用不经过网关中间件 —— 不校验就会出现"拿着 A 园区身份查到 B 园区合同"。
	// 返回 NotFound 而不是 Forbidden, 避免把"该合同存在"这件事泄露出去。
	if req.GetTenantId() != 0 && detail.Contract.TenantId != req.GetTenantId() {
		return nil, errorx.NewError(errorx.ErrNotFound, "合同不存在")
	}

	return &leasingpb.GetContractResp{Contract: toProtoContract(&detail.Contract)}, nil
}

// GetOccupancy 查询园区入驻率.
func (s *LeasingServer) GetOccupancy(ctx context.Context, req *leasingpb.GetOccupancyReq) (*leasingpb.GetOccupancyResp, error) {
	resp, err := lease.NewOccupancyLogic(ctx, s.svcCtx).
		Occupancy(&types.OccupancyReq{TenantId: req.GetTenantId()})
	if err != nil {
		return nil, err
	}

	return &leasingpb.GetOccupancyResp{
		TotalAreaSqm:  resp.TotalAreaSqm,
		LeasedAreaSqm: resp.LeasedAreaSqm,
		OccupancyRate: resp.OccupancyRate,
	}, nil
}

// toProtoContract HTTP DTO -> proto 字段映射.
//
// 逐字段手写而不是用反射/JSON 中转: 字段一多, 反射映射会在改名/改类型时静默丢字段,
// 手写映射一旦漏字段编译期就能发现(Go 的复合字面量不要求写全, 故本函数由单测兜住完整性)。
func toProtoContract(c *types.Contract) *leasingpb.Contract {
	return &leasingpb.Contract{
		Id:              c.Id,
		ContractNo:      c.ContractNo,
		TenantId:        c.TenantId,
		TenantName:      c.TenantName,
		ZoneCode:        c.ZoneCode,
		AreaSqm:         c.AreaSqm,
		MonthlyRent:     c.MonthlyRent, // decimal 字符串, 不做任何数值转换
		Deposit:         c.Deposit,
		StartDate:       c.StartDate,
		EndDate:         c.EndDate,
		Status:          c.Status,
		AutoRenew:       c.AutoRenew != 0, // DTO 用 int32(0/1, 与 DB 列一致), proto 用 bool
		RenewNoticeDays: c.RenewNoticeDays,
	}
}
