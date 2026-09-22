package model

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_HeartbeatDLQCreate 验证心跳死信台账在真实 MySQL 上可写可读.
// 复用同包 realVideoDB(见 camera_model_test.go), 它会先套用 m3_video_mysql_tables.sql,
// 因此本用例同时验证了新增的 video_dlq 建表脚本能在真实 MySQL 上跑通.
func TestIntegration_HeartbeatDLQCreate(t *testing.T) {
	db := realVideoDB(t)
	dlq := NewHeartbeatDLQModel(db)
	ctx := context.Background()

	// 该表由 append 写入且不含 tenant_id, 用设备ID做隔离键, 清理本用例自己的数据.
	const deviceID = "cam-dlq-it"
	if err := db.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&HeartbeatDLQ{}).Error; err != nil {
		t.Fatalf("clean video_dlq: %v", err)
	}

	now := time.Now()
	entry := &HeartbeatDLQ{
		Topic:       "device-telemetry",
		PartitionNo: 3,
		MsgOffset:   42,
		DeviceID:    deviceID,
		Payload:     "{not json",
		ErrorMsg:    "unmarshal device message: invalid character",
		CreatedAt:   now,
	}
	if err := dlq.Create(ctx, entry); err != nil {
		t.Fatalf("写入心跳死信失败: %v", err)
	}
	if entry.ID == 0 {
		t.Error("自增主键应回填")
	}

	var saved HeartbeatDLQ
	if err := db.WithContext(ctx).Where("device_id = ?", deviceID).First(&saved).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if saved.Topic != "device-telemetry" || saved.PartitionNo != 3 || saved.MsgOffset != 42 {
		t.Errorf("定位信息丢失: %+v", saved)
	}
	if saved.Payload != "{not json" || saved.ErrorMsg == "" {
		t.Errorf("原始报文/失败原因未留存: %+v", saved)
	}
	if saved.CreatedAt.IsZero() {
		t.Error("created_at 未落库")
	}
}
