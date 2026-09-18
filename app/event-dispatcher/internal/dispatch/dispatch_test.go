package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"onepark/app/event-dispatcher/internal/archive"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

func TestSplitTopic(t *testing.T) {
	tests := []struct {
		name         string
		topic        string
		wantDeviceID string
		wantKind     string
		wantOK       bool
	}{
		{"事件上报", "onepark/device/pk001/dev-001/event", "dev-001", "event", true},
		{"遥测上报", "onepark/device/pk001/dev-002/telemetry", "dev-002", "telemetry", true},
		{"上下线带斜杠", "/onepark/device/pk001/dev-003/status/", "dev-003", "status", true},
		{"层级不足", "onepark/device/pk001", "", "", false},
		{"空 topic", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceID, kind, ok := SplitTopic(tt.topic)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if deviceID != tt.wantDeviceID {
				t.Errorf("deviceID = %s, 期望 %s", deviceID, tt.wantDeviceID)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %s, 期望 %s", kind, tt.wantKind)
			}
		})
	}
}

// stubArchiveReader 档案读取桩.
type stubArchiveReader struct {
	profiles map[string]archive.Profile
}

func (s *stubArchiveReader) Get(_ context.Context, deviceID string) (archive.Profile, bool, error) {
	p, ok := s.profiles[deviceID]
	return p, ok, nil
}

// fakePublisher 记录投递结果供断言; 可按次数注入失败.
type fakePublisher struct {
	mu        sync.Mutex
	published []fakeMsg
	failFirst int // 前 N 次投递失败
}
type fakeMsg struct {
	topic string
	value string
}

func (f *fakePublisher) Publish(_ context.Context, topic string, key, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) < f.failFirst {
		f.published = append(f.published, fakeMsg{topic: topic, value: ""}) // 占位计失败次数
		return errors.New("broker down")
	}
	f.published = append(f.published, fakeMsg{topic: topic, value: string(value)})
	return nil
}

func (f *fakePublisher) topics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.published {
		out = append(out, m.topic)
	}
	return out
}

// newTestHandler 测试构造: 注入假生产者(短退避)与档案解析器.
func newTestHandler(p Publisher, r *archive.Resolver) *Handler {
	return &Handler{
		Logger:      logx.WithContext(context.Background()),
		publisher:   p,
		resolver:    r,
		retryPauses: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
}

// TestDispatchInvalidTopic 非法 topic 应进 DLQ 且返回 nil(已妥善处理, 不向上抛).
func TestDispatchInvalidTopic(t *testing.T) {
	fp := &fakePublisher{}
	h := newTestHandler(fp, nil)
	if err := h.Dispatch(context.Background(), "onepark/device/pk001", []byte(`{}`)); err != nil {
		t.Fatalf("非法 topic 应吞掉并进 DLQ: %v", err)
	}
	if got := fp.topics(); len(got) != 1 || got[0] != kafka.TopicDispatcherDLQ {
		t.Fatalf("非法 topic 应投 DLQ, got %v", got)
	}
}

// TestDispatchEnrichesTenantZone 正常消息必须带 tenant_id/zone_id 且投遥测 topic.
func TestDispatchEnrichesTenantZone(t *testing.T) {
	fp := &fakePublisher{}
	s := &stubArchiveReader{profiles: map[string]archive.Profile{"d1": {TenantID: 7, ZoneID: "zone-a"}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	err := h.Dispatch(context.Background(),
		"onepark/device/pk_meter/d1/telemetry",
		[]byte(`{"request_id":"r1","device_type":"meter","event_type":"telemetry","occurred_at":100,"payload":{"metrics":{"energy_total":1.5}}}`))
	if err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	msgs := fp.published
	if len(msgs) != 1 || msgs[0].topic != kafka.TopicDeviceTelemetry {
		t.Fatalf("应仅投遥测 topic 1 条, got %v", fp.topics())
	}
	var m kafka.DeviceTelemetry
	if err := json.Unmarshal([]byte(msgs[0].value), &m); err != nil {
		t.Fatalf("消息解码失败: %v", err)
	}
	if m.TenantID != 7 || m.ZoneID != "zone-a" || m.DeviceID != "d1" {
		t.Fatalf("充入字段错误: %+v", m)
	}
}

// TestDispatchAlarmDualPublish 告警类事件必须同时投遥测与告警 topic.
func TestDispatchAlarmDualPublish(t *testing.T) {
	fp := &fakePublisher{}
	s := &stubArchiveReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"fire","payload":{}}`))
	if got := fp.topics(); len(got) != 2 || got[0] != kafka.TopicDeviceTelemetry || got[1] != kafka.TopicAlarm {
		t.Fatalf("告警应双投, got %v", got)
	}
}

// TestDispatchBadMessageToDLQ 解析失败必须进 DLQ 且不返回错误(已妥善处理).
func TestDispatchBadMessageToDLQ(t *testing.T) {
	fp := &fakePublisher{}
	h := newTestHandler(fp, archive.NewResolver(&stubArchiveReader{profiles: map[string]archive.Profile{}}, time.Minute))

	if err := h.Dispatch(context.Background(), "onepark/device/pk/d1/event", []byte(`{bad`)); err != nil {
		t.Fatalf("坏消息应吞掉并进 DLQ, 不向上抛: %v", err)
	}
	if got := fp.topics(); len(got) != 1 || got[0] != kafka.TopicDispatcherDLQ {
		t.Fatalf("坏消息应投 DLQ, got %v", got)
	}
	var env kafka.DLQEnvelope
	_ = json.Unmarshal([]byte(fp.published[0].value), &env)
	if env.Reason == "" || !strings.Contains(env.Raw, "bad") {
		t.Fatalf("DLQ 信封不完整: %+v", env)
	}
}

// TestDispatchUnknownDeviceToDLQ 设备档案查不到必须进 DLQ.
func TestDispatchUnknownDeviceToDLQ(t *testing.T) {
	fp := &fakePublisher{}
	h := newTestHandler(fp, archive.NewResolver(&stubArchiveReader{profiles: map[string]archive.Profile{}}, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/ghost/event",
		[]byte(`{"event_type":"fire","payload":{}}`))
	if got := fp.topics(); len(got) != 1 || got[0] != kafka.TopicDispatcherDLQ {
		t.Fatalf("未知设备应投 DLQ, got %v", got)
	}
}

// TestDispatchPublishRetryThenSuccess 投递失败按退避重试, 成功后不进 DLQ.
func TestDispatchPublishRetryThenSuccess(t *testing.T) {
	fp := &fakePublisher{failFirst: 2} // 前 2 次失败, 第 3 次成功
	s := &stubArchiveReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	if err := h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"telemetry","payload":{}}`)); err != nil {
		t.Fatalf("重试后成功不应报错: %v", err)
	}
	for _, topic := range fp.topics() {
		if topic == kafka.TopicDispatcherDLQ {
			t.Fatal("重试成功不应进 DLQ")
		}
	}
}

// TestDispatchPublishRetryExhaustedToDLQ 重试耗尽必须进 DLQ.
func TestDispatchPublishRetryExhaustedToDLQ(t *testing.T) {
	fp := &fakePublisher{failFirst: 99}
	s := &stubArchiveReader{profiles: map[string]archive.Profile{"d1": {}}}
	h := newTestHandler(fp, archive.NewResolver(s, time.Minute))

	_ = h.Dispatch(context.Background(), "onepark/device/pk/d1/event",
		[]byte(`{"event_type":"telemetry","payload":{}}`))
	found := false
	for _, topic := range fp.topics() {
		if topic == kafka.TopicDispatcherDLQ {
			found = true
		}
	}
	if !found {
		t.Fatalf("重试耗尽应进 DLQ, got %v", fp.topics())
	}
}
