// Package consumer 消费工单事件(workorder-event), 生成站内通知.
//
// 链路: workorder-service(建单/派单/状态流转) → Kafka `workorder-event`
// → 本消费者 → notice(通知主体, 幂等键 source) + notice_read(送达记录).
//
// 站内通知渠道语义:
//   - notice: 通知内容本身, 对应"公告/通知"实体;
//   - notice_read: 按"目标人"落一行送达记录, ReadAt 为 NULL 表示已送达未读,
//     用户查看后回填, 由此可算未读数.
//
// 幂等: notice.source 可空唯一索引(uk_source)按事件去重;
// notice_read 用 (notice_id, user_id) 唯一索引 + OnConflict DoNothing 兜底.
package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// WorkOrderEvent 工单事件载荷, 与生产侧(workorder-service internal/logic.WorkOrderEvent)
// 的 JSON 字段一一对应; 该结构由 M2 自产自销, 若生产侧调整字段需同步本文件.
type WorkOrderEvent struct {
	EventId     string `json:"event_id"`  // 事件ID(全链路 RequestId), 作幂等键
	Event       string `json:"event"`     // created / assigned / status_changed
	Action      string `json:"action"`    // create / assign / submit / approve / reject / close
	TenantId    int64  `json:"tenant_id"` // 园区ID
	WorkOrderId int64  `json:"work_order_id"`
	OrderNo     string `json:"order_no"`
	FromStatus  int8   `json:"from_status"`
	ToStatus    int8   `json:"to_status"`
	OperatorId  int64  `json:"operator_id"` // 操作人
	AssigneeId  int64  `json:"assignee_id"` // 处理人
	Timestamp   int64  `json:"timestamp"`
}

// statusName 工单状态中文名(与 work_order.status 含义一致).
func statusName(s int8) string {
	switch s {
	case 0:
		return "待派单"
	case 1:
		return "处理中"
	case 2:
		return "待验收"
	case 3:
		return "已完成"
	case 4:
		return "已关闭"
	default:
		return fmt.Sprintf("状态%d", s)
	}
}

// NoticeDraft 由工单事件推导出的站内通知草稿(纯函数, 便于单测).
type NoticeDraft struct {
	Source  string // 幂等键, 如 workorder:assigned:{event_id}
	Title   string
	Content string
	// Targets 通知送达的目标用户(去重且排除系统用户0).
	// 为空表示广播型通知(全体可见), 不写送达记录.
	Targets []int64
}

// DecodeWorkOrderEvent 解析一条工单事件.
// 非法消息返回 error, 由调用方记录后跳过(不能让坏消息卡住消费分区).
func DecodeWorkOrderEvent(value []byte) (WorkOrderEvent, error) {
	var ev WorkOrderEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		return WorkOrderEvent{}, fmt.Errorf("解析工单事件失败: %w", err)
	}
	if ev.OrderNo == "" {
		return WorkOrderEvent{}, errors.New("工单事件缺少 order_no")
	}
	if ev.EventId == "" {
		return WorkOrderEvent{}, errors.New("工单事件缺少 event_id, 无法做幂等去重")
	}
	return ev, nil
}

// buildNoticeDraft 把工单事件映射成站内通知草稿.
//
// 通知对象规则:
//   - created  → 报修人(操作人): "你的工单已受理";
//   - assigned → 处理人 + 操作人: "有新工单派给你";
//   - status_changed → 处理人 + 操作人: "工单状态更新".
func buildNoticeDraft(ev WorkOrderEvent) NoticeDraft {
	// 幂等键带 event_id: 同一事件重投只落一条; 不同事件(同工单)各自成条.
	source := fmt.Sprintf("workorder:%s:%s", ev.Event, ev.EventId)
	title := fmt.Sprintf("工单 %s 状态更新", ev.OrderNo)
	content := fmt.Sprintf("工单 %s 状态更新: %s → %s。",
		ev.OrderNo, statusName(ev.FromStatus), statusName(ev.ToStatus))

	switch ev.Event {
	case "created":
		title = fmt.Sprintf("工单 %s 已受理, 等待派单", ev.OrderNo)
		content = fmt.Sprintf("您提交的工单 %s 已受理, 当前状态: 待派单。", ev.OrderNo)
	case "assigned":
		title = fmt.Sprintf("工单 %s 已派单给您", ev.OrderNo)
		content = fmt.Sprintf("工单 %s 已派单, 请及时处理。当前状态: %s。",
			ev.OrderNo, statusName(ev.ToStatus))
	}

	targets := dedupUsers(ev.AssigneeId, ev.OperatorId)
	return NoticeDraft{Source: source, Title: title, Content: content, Targets: targets}
}

// dedupUsers 合并并过滤系统用户(0), 保持稳定顺序.
func dedupUsers(ids ...int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue // 0 是系统, 不是真实收件人
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// noticeAt 统一通知落库时间(秒级事件时间优先, 缺失用当前时间).
func noticeAt(ev WorkOrderEvent, now time.Time) time.Time {
	if ev.Timestamp > 0 {
		return time.Unix(ev.Timestamp, 0)
	}
	return now
}
