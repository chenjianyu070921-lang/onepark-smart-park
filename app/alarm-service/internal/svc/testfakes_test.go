package svc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"

	kafkago "github.com/segmentio/kafka-go"
)

// 本文件存放 svc 包多个测试文件共用的替身与构造助手.
//
// 为什么单独成文件而不是放在 consumer_test.go: consumer_test.go 由 M1 统一契约的回归用例
// 维护(见 TestParseDeviceEvent_UnifiedContract), 是高频改动文件; 共享替身原先写在那里,
// 该文件被整体覆盖后 esindex/dlq 等用例因找不到 fakeAlarmStore 而全部编译失败。
// 替身属于测试基础设施, 与具体用例分离可避免被单个文件的改动连带删除。

// fakeAlarmStore 复刻 MySQL 行为: 按 request_id 去重(L3 唯一索引) + 回填自增主键.
type fakeAlarmStore struct {
	alarms []*model.Alarm
	nextID int64
}

func (f *fakeAlarmStore) Create(_ context.Context, a *model.Alarm) error {
	for _, exist := range f.alarms {
		if exist.RequestID == a.RequestID {
			return model.ErrDuplicateRequest
		}
	}
	// 复刻自增主键: ES 双写按 alarm_id 幂等, 替身不回填主键会让该断言失去意义.
	f.nextID++
	a.ID = f.nextID
	f.alarms = append(f.alarms, a)
	return nil
}

func (f *fakeAlarmStore) FindByID(context.Context, int64, int64) (*model.Alarm, error) {
	return nil, model.ErrNotFound
}

func (f *fakeAlarmStore) List(context.Context, model.AlarmListFilter) ([]*model.Alarm, int64, error) {
	return nil, 0, nil
}

func (f *fakeAlarmStore) Ack(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}

func (f *fakeAlarmStore) Resolve(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}

func (f *fakeAlarmStore) CountActive(context.Context, int64, int64, []int32) (int64, []model.LevelCount, error) {
	return 0, nil, nil
}

func (f *fakeAlarmStore) SearchHistory(context.Context, model.AlarmHistoryFilter) ([]*model.Alarm, int64, []model.LevelCount, error) {
	return nil, 0, nil, nil
}

var _ model.AlarmModel = (*fakeAlarmStore)(nil)

// fakeDeduper 复刻 Redis SetNX 语义: 首次 false, 之后 true.
type fakeDeduper struct {
	seen map[string]bool
}

func (f *fakeDeduper) Seen(_ context.Context, key string) (bool, error) {
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

// Release 供"占键后告警未落库"的失败路径回收幂等键(见 consumer.go#releaseDedupKey):
// 不释放的话重投会被判成重复消息, 告警永久丢失。死信重放前清键则走 Redis 直连(见 ReplayDeadLetter).
func (f *fakeDeduper) Release(_ context.Context, key string) error {
	delete(f.seen, key)
	return nil
}

var _ dedup.Deduper = (*fakeDeduper)(nil)

// fakeCooldown 复刻 Redis SetNX 冷却语义: 同一 key 窗口内首次放行, 之后抑制.
type fakeCooldown struct{ active map[string]bool }

func newFakeCooldown() *fakeCooldown { return &fakeCooldown{active: map[string]bool{}} }

func (f *fakeCooldown) TryAcquire(_ context.Context, key string, _ time.Duration) (bool, error) {
	if f.active[key] {
		return true, nil
	}
	f.active[key] = true
	return false, nil
}

var _ dedup.Cooldown = (*fakeCooldown)(nil)

// fakeEventNotifier 记录两类告警事件, 可注入错误以覆盖"通知失败"分支.
type fakeEventNotifier struct {
	created  []notify.AlarmEvent
	resolved []notify.AlarmEvent
	err      error
}

func (f *fakeEventNotifier) AlarmCreated(_ context.Context, ev notify.AlarmEvent) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, ev)
	return nil
}

func (f *fakeEventNotifier) AlarmResolved(_ context.Context, ev notify.AlarmEvent) error {
	if f.err != nil {
		return f.err
	}
	f.resolved = append(f.resolved, ev)
	return nil
}

var _ notify.Notifier = (*fakeEventNotifier)(nil)

// newTestContext 构造最小可用的消费链路上下文(不装配 ES / 通知器).
func newTestContext() (*ServiceContext, *fakeAlarmStore) {
	store := &fakeAlarmStore{}
	return &ServiceContext{Alarms: store, Dedup: &fakeDeduper{seen: map[string]bool{}}}, store
}

// intrusionEvent 构造一条门禁非法闯入事件的 Kafka 消息.
// AreaID 取 12 是为了让"区域维度是否透传"在下游断言中可被检验.
func intrusionEvent(t *testing.T, requestID string) kafkago.Message {
	t.Helper()
	body, err := json.Marshal(DeviceEvent{
		RequestID:  requestID,
		TenantID:   1,
		DeviceID:   "door-01",
		DeviceType: DeviceTypeAccessControl,
		EventType:  EventTypeIntrusion,
		AreaID:     12,
		OccurredAt: 1758300000,
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return kafkago.Message{Topic: "device-telemetry", Partition: 0, Offset: 1, Value: body}
}
