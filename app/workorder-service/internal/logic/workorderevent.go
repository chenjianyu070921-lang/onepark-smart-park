package logic

import (
	"context"
	"encoding/json"
	"time"

	"onepark/app/workorder-service/internal/svc"
	"onepark/common/ctxdata"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// workorderEventPublishTimeout 事件发布超时, 避免 Kafka 不可用时阻塞主业务流程.
const workorderEventPublishTimeout = 2 * time.Second

// WorkOrderEvent 工单事件载荷(发布到 Kafka topic=workorder-event).
// 供 M5 大屏实时感知与通知类消费, event 区分建单/派单/状态流转.
type WorkOrderEvent struct {
	EventId     string `json:"event_id"`  // 事件ID(优先取全链路 RequestId, 便于幂等去重)
	Event       string `json:"event"`     // created / assigned / status_changed
	Action      string `json:"action"`    // create / assign / submit / approve / reject / close
	TenantId    int64  `json:"tenant_id"` // 园区ID(RBAC 隔离维度)
	WorkOrderId int64  `json:"work_order_id"`
	OrderNo     string `json:"order_no"`
	FromStatus  int8   `json:"from_status"` // 流转前状态(-1 表示建单)
	ToStatus    int8   `json:"to_status"`   // 流转后状态
	OperatorId  int64  `json:"operator_id"` // 操作人(网关注入的 x-user-id)
	AssigneeId  int64  `json:"assignee_id"` // 处理人(派单后)
	Timestamp   int64  `json:"timestamp"`   // 事件时间(秒级)
}

// publishWorkOrderEvent 发布工单事件到 Kafka.
// 采用"尽力而为"语义: 生产者未初始化或发送失败仅记录日志, 绝不阻断/回滚工单主流程.
// 使用独立 context(带短超时)而非请求 context, 避免客户端断开导致事件丢失.
func publishWorkOrderEvent(ctx context.Context, svcCtx *svc.ServiceContext, logger logx.Logger, ev WorkOrderEvent) {
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
		logger.Errorf("marshal workorder-event failed: %v", err)
		return
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), workorderEventPublishTimeout)
	defer cancel()
	// key 用工单号, 保证同一工单事件落到同一分区(顺序可感知).
	if err := svcCtx.Producer.Publish(pubCtx, kafka.TopicWorkorder, []byte(ev.OrderNo), body); err != nil {
		logger.Errorf("publish workorder-event failed: %v", err)
	}
}
