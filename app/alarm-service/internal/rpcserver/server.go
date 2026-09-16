// Package rpcserver 实现 alarm gRPC 服务(M5 运营调度大屏聚合依赖).
// 端口规划 9009; 提供 Ping 探活与 GetActiveAlarms 活跃告警聚合查询.
package rpcserver

import (
	"context"

	"onepark/app/alarm-service/internal/model"
	alarmpb "onepark/proto/alarm"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
)

// AlarmServer 告警 gRPC 服务实现.
type AlarmServer struct {
	alarmpb.UnimplementedAlarmServiceServer
	Alarms model.AlarmModel // 告警数据访问层(MySQL 未配置时为 nil, 返回空聚合)
}

// NewAlarmServer 构造告警 gRPC 服务.
func NewAlarmServer(alarms model.AlarmModel) *AlarmServer {
	return &AlarmServer{Alarms: alarms}
}

// Ping 探活.
func (s *AlarmServer) Ping(_ context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// GetActiveAlarms 供 M5 大屏聚合查询活跃告警总数与按等级分布.
// 活跃告警即 status=0(未处理); area_id/levels 为 0/空时不参与过滤.
func (s *AlarmServer) GetActiveAlarms(ctx context.Context, req *alarmpb.GetActiveAlarmsReq) (*alarmpb.GetActiveAlarmsResp, error) {
	resp := &alarmpb.GetActiveAlarmsResp{LevelCount: map[int32]int64{}}

	// DB 未初始化(本地无 MySQL)时返回空聚合, 保证 gRPC 可启动供 M5 联调.
	if s.Alarms == nil {
		logx.WithContext(ctx).Slowf("alarm rpc: storage not ready, return empty aggregation")
		return resp, nil
	}

	total, counts, err := s.Alarms.CountActive(ctx, req.GetTenantId(), req.GetAreaId(), req.GetLevels())
	if err != nil {
		logx.WithContext(ctx).Errorf("alarm rpc count active failed: %v", err)
		return nil, err
	}

	resp.Total = total
	for _, c := range counts {
		resp.LevelCount[int32(c.Level)] = c.Total
	}
	return resp, nil
}
