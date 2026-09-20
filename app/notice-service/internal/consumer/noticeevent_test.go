package consumer

import (
	"testing"
	"time"
)

// 验证公告发布事件的解码与非法消息拦截(场景2: notice-event 消费).
func TestDecodeNoticeEvent(t *testing.T) {
	pub := time.Unix(1758200000, 0)
	ok := []byte(`{"id":9,"tenant_id":1,"title":"停水通知","type":4,"top":1,"status":2,"publish_at":"` + pub.Format(time.RFC3339Nano) + `"}`)
	ev, err := DecodeNoticeEvent(ok)
	if err != nil {
		t.Fatalf("合法事件不应报错: %v", err)
	}
	if ev.ID != 9 || ev.TenantID != 1 || ev.Type != 4 || ev.Status != 2 || ev.Title != "停水通知" {
		t.Errorf("字段解析不符: %+v", ev)
	}
	if ev.PublishAt == nil || ev.PublishAt.Unix() != 1758200000 {
		t.Errorf("publish_at 解析不符: %+v", ev.PublishAt)
	}

	bad := [][]byte{
		[]byte(`not-json`),               // 非 JSON
		[]byte(`{"title":"无ID"}`),        // 缺 id
		[]byte(`{"id":1,"title":"无租户"}`), // 缺 tenant_id
	}
	for i, b := range bad {
		if _, err := DecodeNoticeEvent(b); err == nil {
			t.Errorf("非法消息 %d 应返回错误", i)
		}
	}
}

// 验证推送频道按园区隔离.
func TestNoticeEventChannel(t *testing.T) {
	if got := noticeEventChannel(7); got != "notice-push:7" {
		t.Errorf("channel = %q, want notice-push:7", got)
	}
}
