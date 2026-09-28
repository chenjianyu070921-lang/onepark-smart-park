package svc

import (
	"context"
	"errors"
	"testing"

	"onepark/app/video-service/internal/config"
	"onepark/app/video-service/internal/model"

	kafkago "github.com/segmentio/kafka-go"
)

// fakeHeartbeatDLQ 记录落台账的内容.
type fakeHeartbeatDLQ struct {
	entries []*model.HeartbeatDLQ
	err     error
}

func (f *fakeHeartbeatDLQ) Create(_ context.Context, d *model.HeartbeatDLQ) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, d)
	return nil
}

var _ model.HeartbeatDLQModel = (*fakeHeartbeatDLQ)(nil)

func heartbeatDLQCtx(cameras model.CameraModel, dlq model.HeartbeatDLQModel) *ServiceContext {
	return &ServiceContext{
		Config:       config.Config{Heartbeat: config.HeartbeatConf{DeviceTypes: []string{"camera"}}},
		Cameras:      cameras,
		DeadLetters:  dlq,
	}
}

// TestHandleDeviceMessage_MalformedGoesToLedger 坏消息要留下可追溯的台账, 而不是只有一行日志.
func TestHandleDeviceMessage_MalformedGoesToLedger(t *testing.T) {
	store := &fakeCameras{}
	dlq := &fakeHeartbeatDLQ{}
	s := heartbeatDLQCtx(store, dlq)

	bad := kafkago.Message{Topic: "device-telemetry", Partition: 3, Offset: 42, Value: []byte("{not json")}
	if err := s.handleDeviceMessage(context.Background(), bad); err != nil {
		t.Fatalf("坏消息不应卡分区: %v", err)
	}
	if len(dlq.entries) != 1 {
		t.Fatalf("坏消息应入台账, 实际 %d 条", len(dlq.entries))
	}
	e := dlq.entries[0]
	if e.Topic != "device-telemetry" || e.PartitionNo != 3 || e.MsgOffset != 42 {
		t.Errorf("定位信息(topic/partition/offset)必须齐全: %+v", e)
	}
	if e.ErrorMsg == "" || e.Payload != "{not json" {
		t.Errorf("原始报文与失败原因都应留存: %+v", e)
	}
	if len(store.touched) != 0 {
		t.Error("坏消息不应登记心跳")
	}
}

// TestHandleDeviceMessage_MissingDeviceIdGoesToLedger 缺 device_id 同样要留痕:
// 这类报文往往意味着上游字段改名, 是在线状态静默停更的前兆.
func TestHandleDeviceMessage_MissingDeviceIdGoesToLedger(t *testing.T) {
	dlq := &fakeHeartbeatDLQ{}
	s := heartbeatDLQCtx(&fakeCameras{}, dlq)

	err := s.handleDeviceMessage(context.Background(),
		msg(t, deviceMessage{DeviceType: "camera"})) // DeviceID 为空
	if err != nil {
		t.Fatalf("缺字段消息不应卡分区: %v", err)
	}
	if len(dlq.entries) != 1 || dlq.entries[0].ErrorMsg == "" {
		t.Fatalf("缺 device_id 应入台账: %+v", dlq.entries)
	}
}

// TestDropMessage_NoLedgerKeepsSilentSkip 未配置台账时退回原有的"仅日志跳过",
// 保证未接入 MySQL 的部署方式行为不变.
func TestDropMessage_NoLedgerKeepsSilentSkip(t *testing.T) {
	s := heartbeatDLQCtx(&fakeCameras{}, nil)
	if err := s.dropMessage(context.Background(), kafkago.Message{Value: []byte("x")}, "", errors.New("boom")); err != nil {
		t.Errorf("无台账时应跳过且不报错: %v", err)
	}
}

// TestDropMessage_LedgerWriteFailureDoesNotBlockPartition 台账写失败也不应卡住分区:
// 心跳过期由 StartOfflineSweeper 兜底自动置离线, 为一条过期心跳重投整条分区不划算.
func TestDropMessage_LedgerWriteFailureDoesNotBlockPartition(t *testing.T) {
	dlq := &fakeHeartbeatDLQ{err: errors.New("db down")}
	s := heartbeatDLQCtx(&fakeCameras{}, dlq)
	if err := s.dropMessage(context.Background(), kafkago.Message{Value: []byte("x")}, "cam-1", errors.New("boom")); err != nil {
		t.Errorf("台账写失败仍应返回 nil 以免毒丸卡分区: %v", err)
	}
}

// TestTruncateForLedger 超长 payload 必须截断, 否则整条台账会因超列宽写不进去,
// 反而丢掉本该留下的线索.
func TestTruncateForLedger(t *testing.T) {
	if got := truncateForLedger("abc", 10); got != "abc" {
		t.Errorf("短文本不应被改变: %q", got)
	}
	if got := truncateForLedger("abcdef", 3); got != "abc" {
		t.Errorf("超长文本应截断到 max: %q", got)
	}
}
