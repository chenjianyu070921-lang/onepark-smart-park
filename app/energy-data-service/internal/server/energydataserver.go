// Package server 实现 energy-data-service 对外提供的 gRPC 接口(供 M5 大屏等内部服务调用)
package server

import (
	"context"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"
	energypb "onepark/proto/energy"

	"onepark/app/energy-data-service/internal/logic"
	"onepark/app/energy-data-service/internal/svc"
)

// EnergyDataServer gRPC 服务实现
type EnergyDataServer struct {
	energypb.UnimplementedEnergyDataServiceServer
	svcCtx *svc.ServiceContext
}

func NewEnergyDataServer(svcCtx *svc.ServiceContext) *EnergyDataServer {
	return &EnergyDataServer{svcCtx: svcCtx}
}

// Ping 健康检查
func (s *EnergyDataServer) Ping(ctx context.Context, req *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// GetDailyReport 接口54: 给 M5 大屏查当日总能耗
func (s *EnergyDataServer) GetDailyReport(ctx context.Context, req *energypb.GetDailyReportRequest) (*energypb.GetDailyReportResponse, error) {
	// 1. 解析日期, 没传就是今天
	start, end, ok := logic.ParseDay(req.GetDate())
	if !ok {
		return nil, status.Error(codes.InvalidArgument, errorx.NewError(errorx.ErrBadRequest, "date 格式不对, 例: 2026-09-15").Error())
	}

	// 2. 总用量: 各设备(期末读数 - 期初读数)求和
	total, err := s.svcCtx.EnergyReading.TotalUsageByDevice(ctx, start, end, req.GetZoneId())
	if err != nil {
		return nil, status.Error(codes.Internal, "查询当日总能耗失败")
	}

	// 3. 最新一条数据的时间(没有数据也不算错, 返回空串)
	updatedAt := ""
	if latest, err := s.svcCtx.EnergyReading.FindLatestAny(ctx, req.GetZoneId()); err == nil {
		updatedAt = latest.ReportedAt.Format("2006-01-02 15:04:05")
	}

	return &energypb.GetDailyReportResponse{
		Date:          start.Format("2006-01-02"),
		ZoneId:        req.GetZoneId(),
		TotalUsageKwh: round2(total),
		UpdatedAt:     updatedAt,
	}, nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
