package svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"
)

// ---------------------------------------------------------------------------
// L1 幂等键生命周期回归(2026-09-22 修复: 占键后落库失败 → 告警被伪装成"重复消息"丢弃)
//
// 缺陷现场: Dedup.Seen 在落库**之前**用 SetNX 占键; 落库失败时函数返回 error,
// 消费端重投 → Seen 命中 → 打一行 "skip duplicated" 后返回 nil 提交位移:
// 告警永久丢失、不进死信台账, 而日志看起来是一次正常去重。
//
// 修复后语义(与 common/dedup 的幂等键语义铁律一致):
//   键存在 == 该告警确实落库了。未落库的失败路径必须 Release, 成功路径必须保留。
// 本文件把这两条都锁死, 任一条被改回去都要立刻红。
// ---------------------------------------------------------------------------

// failingAlarmStore 固定返回落库错误, 用于制造"已占键但告警没落库"的路径.
type failingAlarmStore struct {
	calls int
	err   error
}

func (f *failingAlarmStore) Create(_ context.Context, _ *model.Alarm) error {
	f.calls++
	return f.err
}

func (f *failingAlarmStore) FindByID(context.Context, int64, int64) (*model.Alarm, error) {
	return nil, model.ErrNotFound
}

func (f *failingAlarmStore) List(context.Context, model.AlarmListFilter) ([]*model.Alarm, int64, error) {
	return nil, 0, nil
}

func (f *failingAlarmStore) Ack(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}

func (f *failingAlarmStore) Resolve(context.Context, int64, int64, int64, string, time.Time) error {
	return nil
}

func (f *failingAlarmStore) CountActive(context.Context, int64, int64, []int32) (int64, []model.LevelCount, error) {
	return 0, nil, nil
}

func (f *failingAlarmStore) SearchHistory(context.Context, model.AlarmHistoryFilter) ([]*model.Alarm, int64, []model.LevelCount, error) {
	return nil, 0, nil, nil
}

var _ model.AlarmModel = (*failingAlarmStore)(nil)

// failingCooldown 固定返回错误, 用于制造"冷却组件不可用"的路径.
type failingCooldown struct{ err error }

func (f *failingCooldown) TryAcquire(context.Context, string, time.Duration) (bool, error) {
	return false, f.err
}

var _ dedup.Cooldown = (*failingCooldown)(nil)

// TestDedupKeyReleasedWhenCreateFails 落库失败必须释放 L1 键, 使重投能真正补上告警.
func TestDedupKeyReleasedWhenCreateFails(t *testing.T) {
	ctx := context.Background()
	store := &failingAlarmStore{err: errors.New("mysql down")}
	d := &fakeDeduper{seen: map[string]bool{}}
	s := &ServiceContext{Alarms: store, Dedup: d}

	id := "dedup-release-create"
	msg := intrusionEvent(t, id)

	if err := s.HandleDeviceEvent(ctx, msg); err == nil {
		t.Fatal("落库失败必须返回错误, 否则消费端不会重投")
	}
	if store.calls != 1 {
		t.Fatalf("应只尝试落库 1 次, 实际 %d 次", store.calls)
	}

	// 核心断言: 键必须已释放, 否则重投会被判成"已处理"。
	// 直接查替身的键集合而不走 Seen —— Seen 自身会 SetNX 重新占键, 会把后面的重投变成假阴性。
	if d.seen[keyDedup+id] {
		t.Fatal("落库失败后 L1 幂等键仍被占用: 重投会被判为重复消息, 告警永久丢失且不进死信")
	}

	// 端到端走一遍重投: 换成可用的存储后, 同一条消息必须能落库。
	good := &fakeAlarmStore{}
	s.Alarms = good
	if err := s.HandleDeviceEvent(ctx, msg); err != nil {
		t.Fatalf("重投应成功: %v", err)
	}
	if len(good.alarms) != 1 {
		t.Fatalf("重投后应落库 1 条告警, 实际 %d 条", len(good.alarms))
	}
}

// TestDedupKeyReleasedWhenCooldownUnavailable 冷却组件不可用同样必须释放键(它与落库失败同类).
func TestDedupKeyReleasedWhenCooldownUnavailable(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeduper{seen: map[string]bool{}}
	s := &ServiceContext{
		Alarms:   &fakeAlarmStore{},
		Dedup:    d,
		Cooldown: &failingCooldown{err: errors.New("redis down")},
	}

	id := "dedup-release-cooldown"
	if err := s.HandleDeviceEvent(ctx, intrusionEvent(t, id)); err == nil {
		t.Fatal("冷却组件不可用必须返回错误, 否则会绕过 L2 去重直接落库")
	}
	if d.seen[keyDedup+id] {
		t.Fatal("冷却不可用后 L1 幂等键仍被占用: 告警会在重投时被静默丢弃")
	}
}

// TestDedupKeyKeptAfterAlarmPersisted 成功路径**绝不**释放键: 键的存续即"已落库"的凭证.
func TestDedupKeyKeptAfterAlarmPersisted(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeduper{seen: map[string]bool{}}
	store := &fakeAlarmStore{}
	s := &ServiceContext{Alarms: store, Dedup: d}

	id := "dedup-keep-success"
	if err := s.HandleDeviceEvent(ctx, intrusionEvent(t, id)); err != nil {
		t.Fatalf("首次消费应成功: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("应落库 1 条, 实际 %d 条", len(store.alarms))
	}
	seen, err := d.Seen(ctx, keyDedup+id)
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if !seen {
		t.Fatal("落库成功后 L1 幂等键必须保留, 否则重复投递会产生重复告警")
	}

	// 重复消费同一条: 返回 nil(提交位移)且不新增告警.
	if err := s.HandleDeviceEvent(ctx, intrusionEvent(t, id)); err != nil {
		t.Fatalf("重复消费不应报错: %v", err)
	}
	if len(store.alarms) != 1 {
		t.Fatalf("重复消费不应新增告警, 实际 %d 条", len(store.alarms))
	}
}

// TestStolenDedupKeySilentlyDropsAlarm 反向锁定缺陷机制(变异验证的对照组):
// 键被外部占住(等价于修复前"占键后失败未释放")时, 消费会直接返回 nil 且不落库 ——
// 这正是"告警消失却无任何错误痕迹"的现场, 保留该用例以确保 releaseDedupKey 不是空实现。
func TestStolenDedupKeySilentlyDropsAlarm(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeduper{seen: map[string]bool{}}
	store := &fakeAlarmStore{}
	s := &ServiceContext{Alarms: store, Dedup: d}

	id := "dedup-stolen"
	if _, err := d.Seen(ctx, keyDedup+id); err != nil {
		t.Fatalf("预占键失败: %v", err)
	}
	if err := s.HandleDeviceEvent(ctx, intrusionEvent(t, id)); err != nil {
		t.Fatalf("键已占用时应判为已处理并返回 nil, 实际 %v", err)
	}
	if len(store.alarms) != 0 {
		t.Fatalf("键已占用时不应落库, 实际 %d 条", len(store.alarms))
	}
}
