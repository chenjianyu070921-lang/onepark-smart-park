package rpcserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	alarmpb "onepark/proto/alarm"
	commonpb "onepark/proto/common"
)

// fakeCountActive 返回固定的活跃告警聚合结果, 用于验证 gRPC 层的数据透传.
type fakeCountActive struct {
	total  int64
	counts []model.LevelCount
}

func (f *fakeCountActive) Create(context.Context, *model.Alarm) error { return nil }
func (f *fakeCountActive) FindByID(context.Context, int64, int64) (*model.Alarm, error) {
	return nil, model.ErrNotFound
}
func (f *fakeCountActive) List(context.Context, model.AlarmListFilter) ([]*model.Alarm, int64, error) {
	return nil, 0, nil
}
func (f *fakeCountActive) Ack(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}
func (f *fakeCountActive) Resolve(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}

// SearchHistory 历史检索不在 gRPC 层用例范围内(#41 走 HTTP), 返回空集即可满足接口.
func (f *fakeCountActive) SearchHistory(context.Context, model.AlarmHistoryFilter) ([]*model.Alarm, int64, []model.LevelCount, error) {
	return nil, 0, nil, nil
}

func (f *fakeCountActive) CountActive(_ context.Context, tenantID, areaID int64, levels []int32) (int64, []model.LevelCount, error) {
	// 断言过滤条件被正确透传到持久层.
	if tenantID != 1 {
		return 0, nil, errors.New("tenant_id not passed")
	}
	if areaID != 12 {
		return 0, nil, errors.New("area_id not passed")
	}
	if len(levels) != 2 || levels[0] != 3 || levels[1] != 4 {
		return 0, nil, errors.New("levels not passed")
	}
	return f.total, f.counts, nil
}

// TestGetActiveAlarms 验收 P0-4: M5 可经 gRPC 拿到活跃告警总数与按等级分布.
func TestGetActiveAlarms(t *testing.T) {
	srv := NewAlarmServer(&fakeCountActive{
		total:  8,
		counts: []model.LevelCount{{Level: model.AlarmLevelMajor, Total: 5}, {Level: model.AlarmLevelCritical, Total: 3}},
	})

	resp, err := srv.GetActiveAlarms(context.Background(), &alarmpb.GetActiveAlarmsReq{
		TenantId: 1,
		AreaId:   12,
		Levels:   []int32{3, 4},
	})
	if err != nil {
		t.Fatalf("GetActiveAlarms: %v", err)
	}
	if resp.GetTotal() != 8 {
		t.Errorf("期望活跃告警总数 8, 实际 %d", resp.GetTotal())
	}
	if got := resp.GetLevelCount()[3]; got != 5 {
		t.Errorf("期望等级 3 计数 5, 实际 %d", got)
	}
	if got := resp.GetLevelCount()[4]; got != 3 {
		t.Errorf("期望等级 4 计数 3, 实际 %d", got)
	}
}

// TestGetActiveAlarms_StorageNilDegrade MySQL 未配置时 gRPC 仍返回空聚合而非报错, 保证 M5 可联调.
func TestGetActiveAlarms_StorageNilDegrade(t *testing.T) {
	resp, err := NewAlarmServer(nil).GetActiveAlarms(context.Background(), &alarmpb.GetActiveAlarmsReq{TenantId: 1})
	if err != nil {
		t.Fatalf("存储未就绪不应返回错误: %v", err)
	}
	if resp.GetTotal() != 0 || len(resp.GetLevelCount()) != 0 {
		t.Errorf("期望空聚合, 实际 total=%d levels=%v", resp.GetTotal(), resp.GetLevelCount())
	}
}

// TestPing 探活.
func TestPing(t *testing.T) {
	if _, err := NewAlarmServer(nil).Ping(context.Background(), &commonpb.Empty{}); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
