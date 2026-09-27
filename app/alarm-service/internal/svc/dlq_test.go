package svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/model"

	kafkago "github.com/segmentio/kafka-go"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type fakeDLQStore struct {
	entries  []*model.AlarmDLQ
	err      error
	notFound bool // MarkReplayed 返回"记录不存在"
}

func (f *fakeDLQStore) Create(_ context.Context, d *model.AlarmDLQ) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, d)
	return nil
}

func (f *fakeDLQStore) FindByID(_ context.Context, _, id int64) (*model.AlarmDLQ, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, d := range f.entries {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, model.ErrDLQNotFound
}

func (f *fakeDLQStore) List(_ context.Context, _ model.DeadLetterListFilter) ([]*model.AlarmDLQ, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.entries, int64(len(f.entries)), nil
}

// MarkReplayed 记录状态变更; notFound 为 true 时模拟"记录不存在".
func (f *fakeDLQStore) MarkReplayed(_ context.Context, _, id int64) error {
	if f.notFound {
		return model.ErrDLQNotFound
	}
	if f.err != nil {
		return f.err
	}
	for _, d := range f.entries {
		if d.ID == id {
			d.Status = model.DLQStatusReplayed
			return nil
		}
	}
	return model.ErrDLQNotFound
}

var _ model.DeadLetterModel = (*fakeDLQStore)(nil)

// flakyDeduper 前 failTimes 次返回错误(模拟 Redis 抖动), 之后恢复正常.
type flakyDeduper struct {
	calls     int
	failTimes int
}

func (f *flakyDeduper) Seen(context.Context, string) (bool, error) {
	f.calls++
	if f.calls <= f.failTimes {
		return false, errors.New("redis timeout")
	}
	return false, nil
}

// Release 供"占键后告警未落库"的失败路径回收幂等键(见 consumer.go#releaseDedupKey),
// 死信重放前清键另走 Redis 直连(见 ReplayDeadLetter)。本替身恒成功, 不参与抖动模拟。
func (f *flakyDeduper) Release(context.Context, string) error { return nil }

var _ dedup.Deduper = (*flakyDeduper)(nil)

// newDLQCtx 构造死信测试上下文.
// dlq 参数必须是接口类型: 若用 *fakeDLQStore 传 nil, 赋给接口后接口非 nil(Go 经典陷阱),
// 会导致"台账未配置"的分支根本测不到.
func newDLQCtx(dlq model.DeadLetterModel, d dedup.Deduper) *ServiceContext {
	return &ServiceContext{
		Alarms:      &fakeAlarmStore{},
		Cooldown:    newFakeCooldown(),
		Dedup:       d,
		DeadLetters: dlq,
	}
}

func kafkaMsg(body string) kafkago.Message {
	return kafkago.Message{Topic: "device-telemetry", Partition: 3, Offset: 42, Value: []byte(body)}
}

// ---------------------------------------------------------------------------
// 死信与重试
// ---------------------------------------------------------------------------

// TestHandleWithDeadLetter_MalformedNoRetry 坏消息不重试, 直接落台账并提交位移(返回 nil).
func TestHandleWithDeadLetter_MalformedNoRetry(t *testing.T) {
	dlq := &fakeDLQStore{}
	dedupStub := &flakyDeduper{} // 若被错误地重试, 调用次数会 > 0
	svcCtx := newDLQCtx(dlq, dedupStub)

	if err := svcCtx.HandleWithDeadLetter(context.Background(), kafkaMsg(`{"request_id":`)); err != nil {
		t.Fatalf("坏消息应提交位移(返回 nil), 实际 %v", err)
	}
	if len(dlq.entries) != 1 {
		t.Fatalf("应写入 1 条死信, 实际 %d 条", len(dlq.entries))
	}
	if dlq.entries[0].RetryCount != 0 {
		t.Errorf("坏消息不应重试, retry_count=%d", dlq.entries[0].RetryCount)
	}
	if dlq.entries[0].Status != model.DLQStatusPending {
		t.Errorf("死信初始状态应为待处理, 实际 %d", dlq.entries[0].Status)
	}
	if dlq.entries[0].PartitionNo != 3 || dlq.entries[0].MsgOffset != 42 {
		t.Errorf("死信应记录原始定位信息: %+v", dlq.entries[0])
	}
	if dedupStub.calls != 0 {
		t.Errorf("坏消息不应进入处理流程(去重被调用 %d 次)", dedupStub.calls)
	}
}

// TestHandleWithDeadLetter_RetryableThenSuccess 瞬时故障经重试后成功: 不写死信.
func TestHandleWithDeadLetter_RetryableThenSuccess(t *testing.T) {
	dlq := &fakeDLQStore{}
	svcCtx := newDLQCtx(dlq, &flakyDeduper{failTimes: 1}) // 首次 Redis 抖动, 第二次恢复

	if err := svcCtx.HandleWithDeadLetter(context.Background(), intrusionEvent(t, "rid-retry")); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if len(dlq.entries) != 0 {
		t.Errorf("重试成功不应写死信, 实际 %d 条", len(dlq.entries))
	}
}

// TestHandleWithDeadLetter_RetryExhausted 可重试错误耗尽重试后落台账, 且记录重试次数.
func TestHandleWithDeadLetter_RetryExhausted(t *testing.T) {
	dlq := &fakeDLQStore{}
	dedupStub := &flakyDeduper{failTimes: 99} // 一直失败
	svcCtx := newDLQCtx(dlq, dedupStub)

	if err := svcCtx.HandleWithDeadLetter(context.Background(), intrusionEvent(t, "rid-exhaust")); err != nil {
		t.Fatalf("死信落库后应提交位移(返回 nil), 实际 %v", err)
	}
	if len(dlq.entries) != 1 {
		t.Fatalf("应写入 1 条死信, 实际 %d 条", len(dlq.entries))
	}
	if dlq.entries[0].RetryCount != len(retryBackoff) {
		t.Errorf("重试次数应为退避档数 %d, 实际 %d", len(retryBackoff), dlq.entries[0].RetryCount)
	}
	if dlq.entries[0].DeviceID != "door-01" || dlq.entries[0].EventType != EventTypeIntrusion {
		t.Errorf("死信应补全设备与事件信息: %+v", dlq.entries[0])
	}
	if dlq.entries[0].ErrorMsg == "" {
		t.Error("死信应记录失败原因")
	}
	// 总重试耗时必须远小于 rebalance timeout(60s), 否则会触发无谓重平衡.
	var total time.Duration
	for _, d := range retryBackoff {
		total += d
	}
	if total > 10*time.Second {
		t.Errorf("重试退避总时长 %s 过长", total)
	}
}

// TestHandleWithDeadLetter_StoreUnavailable 台账不可用时必须报错, 让消息保持未提交(不静默丢).
func TestHandleWithDeadLetter_StoreUnavailable(t *testing.T) {
	svcCtx := newDLQCtx(nil, &flakyDeduper{failTimes: 99})

	err := svcCtx.HandleWithDeadLetter(context.Background(), intrusionEvent(t, "rid-nodlq"))
	if err == nil {
		t.Fatal("台账不可用时必须返回错误(否则消息被静默丢弃)")
	}
}

// TestHandleWithDeadLetter_WriteFailed 台账写入失败同样不能假装成功.
func TestHandleWithDeadLetter_WriteFailed(t *testing.T) {
	dlq := &fakeDLQStore{err: errors.New("mysql down")}
	svcCtx := newDLQCtx(dlq, &flakyDeduper{failTimes: 99})

	if err := svcCtx.HandleWithDeadLetter(context.Background(), intrusionEvent(t, "rid-dlqfail")); err == nil {
		t.Fatal("死信写入失败应返回错误")
	}
}

// TestHandleWithDeadLetter_ContextCanceled 重试期间 ctx 取消应立即返回, 不做无谓等待.
func TestHandleWithDeadLetter_ContextCanceled(t *testing.T) {
	dlq := &fakeDLQStore{}
	svcCtx := newDLQCtx(dlq, &flakyDeduper{failTimes: 99})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消: 首次处理失败后进入退避等待时应直接退出

	start := time.Now()
	if err := svcCtx.HandleWithDeadLetter(ctx, intrusionEvent(t, "rid-cancel")); err == nil {
		t.Fatal("ctx 取消应返回错误")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ctx 取消后不应继续退避等待, 耗时 %s", elapsed)
	}
}

// TestTruncate 超长错误信息截断, 避免超过列宽导致死信写不进去.
func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("短文本不应被截断: %s", got)
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Errorf("期望截断为 abc, 实际 %s", got)
	}
}
