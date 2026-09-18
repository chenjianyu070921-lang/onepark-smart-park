// visitorevent.go 访客域事件生产(评审 P1): invite/checkin/checkout → Kafka topic=visitor-event.
// 供 M5 大屏等消费方实时感知访客动态; 结构与 workorder-event 范式保持一致(event_id 优先取
// 全链路 RequestId 便于幂等去重, timestamp 秒级), 便于消费端复用同一套解码习惯.
package logic

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"onepark/app/visitor-service/internal/svc"
	"onepark/common/ctxdata"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// visitorEventPublishTimeout 事件发布超时, 避免 Kafka 不可用时阻塞主业务流程.
const visitorEventPublishTimeout = 2 * time.Second

// VisitorEvent 访客事件载荷(发布到 Kafka topic=visitor-event).
// event 取值: invite(邀请签发) / checkin(签入核销) / checkout(签出离场).
type VisitorEvent struct {
	EventId      string `json:"event_id"`      // 事件ID(优先取全链路 RequestId, 便于幂等去重)
	Event        string `json:"event"`         // invite / checkin / checkout
	TenantId     int64  `json:"tenant_id"`     // 园区ID(RBAC 隔离维度)
	VisitorId    int64  `json:"visitor_id"`    // 访客通行记录ID
	VisitorName  string `json:"visitor_name"`  // 访客姓名
	VisitorPhone string `json:"visitor_phone"` // 访客手机号
	InviterId    int64  `json:"inviter_id"`    // 邀请人(业主/物业)
	Status       int8   `json:"status"`        // 事件后记录状态(1待使用/2已签入/3已签出/4已过期)
	DeviceId     string `json:"device_id"`     // 开门设备ID(签入/签出时; 邀请为空)
	Timestamp    int64  `json:"timestamp"`     // 事件时间(秒级)
}

// publishVisitorEvent 发布访客事件到 Kafka.
// 采用"尽力而为"语义: 生产者未初始化或发送失败仅记录日志, 绝不阻断/回滚访客主流程.
// 使用独立 context(带短超时)而非请求 context, 避免客户端断开导致事件丢失.
func publishVisitorEvent(ctx context.Context, svcCtx *svc.ServiceContext, logger logx.Logger, ev VisitorEvent) {
	if svcCtx.Producer == nil {
		return
	}
	if ev.EventId == "" {
		ev.EventId = ctxdata.GetRequestId(ctx)
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().Unix()
	}

	body, err := json.Marshal(ev)
	if err != nil {
		logger.Errorf("marshal visitor-event failed: %v", err)
		return
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), visitorEventPublishTimeout)
	defer cancel()
	// key 用访客记录ID, 保证同一访客的事件落到同一分区(顺序可感知).
	if err := svcCtx.Producer.Publish(pubCtx, kafka.TopicVisitor, []byte(formatVisitorKey(ev.VisitorId)), body); err != nil {
		logger.Errorf("publish visitor-event failed: %v", err)
	}
}

// formatVisitorKey 将访客记录ID转为事件分区键.
func formatVisitorKey(visitorId int64) string {
	return strconv.FormatInt(visitorId, 10)
}
