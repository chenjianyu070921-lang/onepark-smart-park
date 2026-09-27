package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type dlqFakeStore struct {
	entries  []*model.AlarmDLQ
	findErr  error
	markErr  error
	markedID int64
}

func (f *dlqFakeStore) Create(_ context.Context, d *model.AlarmDLQ) error {
	f.entries = append(f.entries, d)
	return nil
}

func (f *dlqFakeStore) FindByID(_ context.Context, _, id int64) (*model.AlarmDLQ, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	for _, d := range f.entries {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, model.ErrDLQNotFound
}

func (f *dlqFakeStore) List(_ context.Context, _ model.DeadLetterListFilter) ([]*model.AlarmDLQ, int64, error) {
	return f.entries, int64(len(f.entries)), nil
}

func (f *dlqFakeStore) MarkReplayed(_ context.Context, _, id int64) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.markedID = id
	for _, d := range f.entries {
		if d.ID == id {
			d.Status = model.DLQStatusReplayed
		}
	}
	return nil
}

var _ model.DeadLetterModel = (*dlqFakeStore)(nil)

// dlqFakeDeduper 模拟 L1 去重; err 非空即"Redis 不可用", 消费链路应报错而非放行.
type dlqFakeDeduper struct{ err error }

func (d *dlqFakeDeduper) Seen(context.Context, string) (bool, error) {
	return false, d.err
}

// Release 告警链路不使用释放, 仅为满足 dedup.Deduper 契约.
func (d *dlqFakeDeduper) Release(context.Context, string) error { return nil }

// replayablePayload 一条可被主链路处理的门禁闯入事件(引擎未配置时命中硬编码规则).
const replayablePayload = `{"request_id":"rq-dlq-1","tenant_id":10,"device_id":"dev-1",` +
	`"device_type":"access_control","event_type":"intrusion","area_id":1,"timestamp":1}`

func newDLQEntry(id int64) *model.AlarmDLQ {
	now := time.Now()
	return &model.AlarmDLQ{
		ID:        id,
		TenantID:  10,
		Topic:     "device-telemetry",
		RequestID: "rq-dlq-1",
		DeviceID:  "dev-1",
		EventType: "intrusion",
		Payload:   replayablePayload,
		ErrorMsg:  "storage unavailable",
		Status:    model.DLQStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func newDLQCtx(store *dlqFakeStore, deduper *dlqFakeDeduper) *svc.ServiceContext {
	return &svc.ServiceContext{
		DeadLetters: store,
		Alarms:      &fakeQueryStore{},
		Dedup:       deduper,
	}
}

// ---------------------------------------------------------------------------
// 列表
// ---------------------------------------------------------------------------

func TestListDeadLetters_MissingTenant(t *testing.T) {
	l := NewListDeadLettersLogic(context.Background(), newDLQCtx(&dlqFakeStore{}, &dlqFakeDeduper{}))
	if _, err := l.ListDeadLetters(&types.ListDLQReq{}); err == nil {
		t.Fatal("缺少租户应报错")
	} else if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("应返回 M3-W-1001, 实际 %v", err)
	}
}

func TestListDeadLetters_StorageNil(t *testing.T) {
	l := NewListDeadLettersLogic(tenantCtx(10), &svc.ServiceContext{})
	_, err := l.ListDeadLetters(&types.ListDLQReq{})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrDepConnect {
		t.Errorf("台账未就绪应返回依赖错误, 实际 %v", err)
	}
}

func TestListDeadLetters_StatusInvalid(t *testing.T) {
	l := NewListDeadLettersLogic(tenantCtx(10), newDLQCtx(&dlqFakeStore{}, &dlqFakeDeduper{}))
	_, err := l.ListDeadLetters(&types.ListDLQReq{Status: 9})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("非法 status 应返回参数错误, 实际 %v", err)
	}
}

func TestListDeadLetters_Success(t *testing.T) {
	store := &dlqFakeStore{entries: []*model.AlarmDLQ{newDLQEntry(1), newDLQEntry(2)}}
	store.entries[1].Status = model.DLQStatusReplayed
	l := NewListDeadLettersLogic(tenantCtx(10), newDLQCtx(store, &dlqFakeDeduper{}))

	resp, err := l.ListDeadLetters(&types.ListDLQReq{Status: -1})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if resp.Total != 2 || len(resp.List) != 2 {
		t.Fatalf("条数不符: total=%d len=%d", resp.Total, len(resp.List))
	}
	// 未传分页时补默认值, 避免返回 page=0 让前端无从渲染.
	if resp.Page != 1 || resp.PageSize != 10 {
		t.Errorf("分页默认值异常: page=%d size=%d", resp.Page, resp.PageSize)
	}
	if resp.List[0].Payload == "" || resp.List[0].ErrorMsg != "storage unavailable" {
		t.Errorf("原始报文/失败原因未透出: %+v", resp.List[0])
	}
	if resp.List[1].Status != model.DLQStatusReplayed {
		t.Errorf("状态映射异常: %+v", resp.List[1])
	}
}

// ---------------------------------------------------------------------------
// 重放
// ---------------------------------------------------------------------------

func TestReplayDeadLetter_ParamGuard(t *testing.T) {
	ctx := newDLQCtx(&dlqFakeStore{}, &dlqFakeDeduper{})
	if _, err := NewReplayDeadLetterLogic(context.Background(), ctx).
		ReplayDeadLetter(&types.IdReq{Id: 1}); err == nil {
		t.Error("缺少租户应报错")
	}
	// 存储未就绪(MySQL 没配)不能给出"已重放"的假象.
	if _, err := NewReplayDeadLetterLogic(tenantCtx(10), &svc.ServiceContext{DeadLetters: &dlqFakeStore{}}).
		ReplayDeadLetter(&types.IdReq{Id: 1}); err == nil {
		t.Error("Alarms 未就绪应报错")
	}
	l := NewReplayDeadLetterLogic(tenantCtx(10), ctx)
	_, err := l.ReplayDeadLetter(&types.IdReq{Id: 0})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("死信ID非法应返回参数错误, 实际 %v", err)
	}
}

func TestReplayDeadLetter_NotFound(t *testing.T) {
	l := NewReplayDeadLetterLogic(tenantCtx(10), newDLQCtx(&dlqFakeStore{}, &dlqFakeDeduper{}))
	_, err := l.ReplayDeadLetter(&types.IdReq{Id: 999})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmDLQNotFound {
		t.Errorf("不存在应返回 M3-E-1009, 实际 %v", err)
	}
}

func TestReplayDeadLetter_Success(t *testing.T) {
	store := &dlqFakeStore{entries: []*model.AlarmDLQ{newDLQEntry(7)}}
	l := NewReplayDeadLetterLogic(tenantCtx(10), newDLQCtx(store, &dlqFakeDeduper{}))

	resp, err := l.ReplayDeadLetter(&types.IdReq{Id: 7})
	if err != nil {
		t.Fatalf("重放失败: %v", err)
	}
	if !resp.Replayed {
		t.Error("重放成功应返回 replayed=true")
	}
	if store.markedID != 7 || store.entries[0].Status != model.DLQStatusReplayed {
		t.Errorf("重放成功后台账应置为已重放: marked=%d status=%d", store.markedID, store.entries[0].Status)
	}
}

// TestReplayDeadLetter_FailedKeepsPending 重放失败时台账必须保持"待处理":
// 若此时标记为已重放, 这条死信就再也找不回来了 —— 死信的价值正在于失败可见.
func TestReplayDeadLetter_FailedKeepsPending(t *testing.T) {
	store := &dlqFakeStore{entries: []*model.AlarmDLQ{newDLQEntry(8)}}
	deduper := &dlqFakeDeduper{err: errors.New("redis timeout")}
	l := NewReplayDeadLetterLogic(tenantCtx(10), newDLQCtx(store, deduper))

	_, err := l.ReplayDeadLetter(&types.IdReq{Id: 8})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmDLQReplay {
		t.Fatalf("应返回 M3-E-1010, 实际 %v", err)
	}
	if store.markedID != 0 {
		t.Error("重放失败不应更新台账状态")
	}
	if store.entries[0].Status != model.DLQStatusPending {
		t.Errorf("应保持待处理, 实际 %d", store.entries[0].Status)
	}
}

// TestReplayDeadLetter_MarkFailedReports 主链路成功但状态回写失败必须报错,
// 否则同一条死信会被反复重放(每次都真实产生告警).
func TestReplayDeadLetter_MarkFailedReports(t *testing.T) {
	store := &dlqFakeStore{
		entries: []*model.AlarmDLQ{newDLQEntry(9)},
		markErr: errors.New("db down"),
	}
	l := NewReplayDeadLetterLogic(tenantCtx(10), newDLQCtx(store, &dlqFakeDeduper{}))

	_, err := l.ReplayDeadLetter(&types.IdReq{Id: 9})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmDLQReplay {
		t.Fatalf("状态回写失败应返回 M3-E-1010, 实际 %v", err)
	}
}
