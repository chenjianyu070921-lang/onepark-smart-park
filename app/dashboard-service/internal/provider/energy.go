package provider

import (
	"context"

	energypb "onepark/proto/energy"
)

// Energy 是基于 M4 energy-data-service gRPC 契约的真实实现.
type Energy struct {
	client energypb.EnergyDataServiceClient
}

// NewEnergy 用 M4 的 gRPC 客户端构造能耗数据源.
func NewEnergy(client energypb.EnergyDataServiceClient) *Energy {
	return &Energy{client: client}
}

// Stat 取当日全园区总用电.
//
// date 传空 = 今天, 由 M4 侧解析(M4 契约: "空表示今天"); zone_id 空 = 全园区。
//
// 注意: M4 的 GetDailyReportResponse 当前只提供 total_usage_kwh,
// **没有水耗字段** -> TotalWater 保持 nil, 绝不用 0 冒充"没用水"
// (0 会被大屏读成"园区停水了", 与 avg_handle_sec 的处理原则一致)。
func (p *Energy) Stat(ctx context.Context) (EnergyStat, error) {
	resp, err := p.client.GetDailyReport(ctx, &energypb.GetDailyReportRequest{})
	if err != nil {
		return EnergyStat{}, err
	}
	return EnergyStat{TotalKwh: resp.GetTotalUsageKwh()}, nil
}
