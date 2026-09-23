// visitorevent_test.go 黑名单拦截事件单测(看板 P2: 黑名单命中后的事件通知链路完善).
// 覆盖: blocked 事件字段构造(签入阶段/邀请阶段) + 生产者未初始化时的尽力而为语义.
package logic

import (
	"context"
	"testing"

	"onepark/app/visitor-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

// TestBuildBlockedEvent_CheckinStage 签入阶段拦截: 事件类型/租户/访客ID/邀请人/姓名/手机号/记录状态逐项透传.
func TestBuildBlockedEvent_CheckinStage(t *testing.T) {
	ev := buildBlockedEvent(5, 100, 7, "张三", "13800000000", 1)
	if ev.Event != "blocked" {
		t.Errorf("Event = %q, want blocked", ev.Event)
	}
	if ev.TenantId != 5 || ev.VisitorId != 100 || ev.InviterId != 7 {
		t.Errorf("ids = (%d,%d,%d), want (5,100,7)", ev.TenantId, ev.VisitorId, ev.InviterId)
	}
	if ev.VisitorName != "张三" || ev.VisitorPhone != "13800000000" {
		t.Errorf("name/phone = (%q,%q), want (张三,13800000000)", ev.VisitorName, ev.VisitorPhone)
	}
	if ev.Status != 1 {
		t.Errorf("Status = %d, want 1(记录既有状态)", ev.Status)
	}
	if ev.DeviceId != "" {
		t.Errorf("DeviceId = %q, want 空(未放行无开门设备)", ev.DeviceId)
	}
}

// TestBuildBlockedEvent_InviteStage 邀请阶段拦截: visitor_id=0 且状态为 0(通行记录尚未创建).
func TestBuildBlockedEvent_InviteStage(t *testing.T) {
	ev := buildBlockedEvent(5, 0, 7, "李四", "13900000000", 0)
	if ev.Event != "blocked" {
		t.Errorf("Event = %q, want blocked", ev.Event)
	}
	if ev.VisitorId != 0 || ev.Status != 0 {
		t.Errorf("VisitorId/Status = (%d,%d), want (0,0)", ev.VisitorId, ev.Status)
	}
}

// TestPublishVisitorEvent_NilProducer 生产者未初始化时发布应静默跳过(尽力而为语义, 不得 panic).
func TestPublishVisitorEvent_NilProducer(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publishVisitorEvent with nil producer panicked: %v", r)
		}
	}()
	publishVisitorEvent(context.Background(), &svc.ServiceContext{}, logx.WithContext(context.Background()),
		buildBlockedEvent(5, 0, 7, "王五", "13700000000", 0))
}
