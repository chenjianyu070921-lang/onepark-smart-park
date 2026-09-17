package consumer

import (
	"encoding/json"
	"testing"
)

// validEvent 构造一条合法工单事件.
func validEvent(t *testing.T, override map[string]interface{}) []byte {
	t.Helper()
	m := map[string]interface{}{
		"event_id":      "req-abc-001",
		"event":         "assigned",
		"action":        "assign",
		"tenant_id":     1,
		"work_order_id": 9,
		"order_no":      "WO-20260916-0001",
		"from_status":   0,
		"to_status":     1,
		"operator_id":   3,
		"assignee_id":   7,
		"timestamp":     1726000000,
	}
	for k, v := range override {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return b
}

func TestDecodeWorkOrderEvent_OK(t *testing.T) {
	ev, err := DecodeWorkOrderEvent(validEvent(t, nil))
	if err != nil {
		t.Fatalf("expect nil err, got %v", err)
	}
	if ev.OrderNo != "WO-20260916-0001" || ev.Event != "assigned" || ev.AssigneeId != 7 {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestDecodeWorkOrderEvent_MissingOrderNo(t *testing.T) {
	if _, err := DecodeWorkOrderEvent(validEvent(t, map[string]interface{}{"order_no": ""})); err == nil {
		t.Fatal("expect error for missing order_no, got nil")
	}
}

func TestDecodeWorkOrderEvent_MissingEventID(t *testing.T) {
	// event_id 是幂等键, 缺失必须拒绝, 否则重投会生成重复通知.
	if _, err := DecodeWorkOrderEvent(validEvent(t, map[string]interface{}{"event_id": ""})); err == nil {
		t.Fatal("expect error for missing event_id, got nil")
	}
}

func TestDecodeWorkOrderEvent_BadJSON(t *testing.T) {
	if _, err := DecodeWorkOrderEvent([]byte("not-json")); err == nil {
		t.Fatal("expect error for invalid json, got nil")
	}
}

func TestBuildNoticeDraft_Assigned(t *testing.T) {
	ev, _ := DecodeWorkOrderEvent(validEvent(t, nil))
	draft := buildNoticeDraft(ev)

	if draft.Source != "workorder:assigned:req-abc-001" {
		t.Fatalf("unexpected source: %s", draft.Source)
	}
	// 派单通知应送达处理人(7)与操作人(3), 且去重排除系统用户.
	if len(draft.Targets) != 2 {
		t.Fatalf("expect 2 targets, got %v", draft.Targets)
	}
	if draft.Targets[0] != 7 || draft.Targets[1] != 3 {
		t.Fatalf("expect [assignee, operator], got %v", draft.Targets)
	}
	if draft.Title == "" || draft.Content == "" {
		t.Fatalf("title/content should not be empty: %q / %q", draft.Title, draft.Content)
	}
}

func TestBuildNoticeDraft_Created(t *testing.T) {
	ev, _ := DecodeWorkOrderEvent(validEvent(t, map[string]interface{}{
		"event":       "created",
		"action":      "create",
		"from_status": -1,
		"to_status":   0,
		"assignee_id": 0, // 建单时无处理人
	}))
	draft := buildNoticeDraft(ev)

	if draft.Source != "workorder:created:req-abc-001" {
		t.Fatalf("unexpected source: %s", draft.Source)
	}
	// 建单通知只有报修人(操作人), 处理人 0 必须被过滤.
	if len(draft.Targets) != 1 || draft.Targets[0] != 3 {
		t.Fatalf("expect only operator target, got %v", draft.Targets)
	}
}

func TestBuildNoticeDraft_DedupAndExcludeSystemUser(t *testing.T) {
	// 派单人=处理人的场景: 去重后只剩一个; 0(系统)必须排除.
	ev, _ := DecodeWorkOrderEvent(validEvent(t, map[string]interface{}{
		"operator_id": 7,
		"assignee_id": 7,
	}))
	draft := buildNoticeDraft(ev)
	if len(draft.Targets) != 1 || draft.Targets[0] != 7 {
		t.Fatalf("expect dedup to single target 7, got %v", draft.Targets)
	}

	ev2, _ := DecodeWorkOrderEvent(validEvent(t, map[string]interface{}{
		"operator_id": 0,
		"assignee_id": 0,
	}))
	if got := buildNoticeDraft(ev2).Targets; len(got) != 0 {
		t.Fatalf("expect no targets for system-only users, got %v", got)
	}
}

func TestStatusName(t *testing.T) {
	cases := map[int8]string{
		0: "待派单", 1: "处理中", 2: "待验收", 3: "已完成", 4: "已关闭", 9: "状态9",
	}
	for in, want := range cases {
		if got := statusName(in); got != want {
			t.Fatalf("statusName(%d)=%q, want %q", in, got, want)
		}
	}
}
