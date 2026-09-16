// Package consumer 消费共享 Kafka 上的告警事件(alarm-event), 自动生成报修工单.
//
// 数据源: M1 event-dispatcher 投递到 `alarm-event` 的告警消息(见 common/kafka/topics.go)。
// 幂等: 复用 dispatch-service 的 uk_alarm_id 唯一索引模式 —— work_order.alarm_id 可空唯一,
// 同一告警重复投递/多实例并发消费只会落一张工单, 人工创建的工单(alarm_id 为 NULL)互不影响.
// 使用纪律: 共享 broker 为多项目共用, 消费组需带项目+服务+环境, 且消费者默认关闭
// —— 见 internal/config.KafkaConf 注释.
package consumer

import (
	"encoding/json"
	"errors"
	"fmt"

	"onepark/app/workorder-service/internal/model"
)

// AlarmEvent 是 M1 event-dispatcher 投递到 alarm-event 的告警消息体.
//
// 字段与 app/event-dispatcher/internal/dispatch.Message 对齐(与 dispatch-service 侧一致).
// ⚠️ 该结构目前由 M1 单方面定义、尚未下沉到 common 包, 若 M1 调整字段需同步本文件.
type AlarmEvent struct {
	RequestID  string          `json:"request_id"`  // 告警请求ID, 作为建单幂等键(alarm_id)
	DeviceID   string          `json:"device_id"`   // 触发告警的设备
	DeviceType string          `json:"device_type"` // 设备类型
	EventType  string          `json:"event_type"`  // fire/smoke/intrusion/door_force/fault/offline_alert...
	OccurredAt int64           `json:"occurred_at"` // 告警发生时间(秒级)
	Payload    json.RawMessage `json:"payload"`     // 附加信息(位置/租户等)
	Source     string          `json:"source"`      // 事件来源
}

// eventMetaT 告警类型对应的工单标题与优先级.
// 优先级取值与 work_order.priority 一致: 1 紧急 / 2 普通 / 3 低.
type eventMetaT struct {
	Title    string
	Priority int8
}

// eventMeta 告警类型映射表. 取值与 M1 event-dispatcher 的 alarmEventTypes 对齐.
// 人身/财产安全类(fire/smoke/intrusion/door_force)按紧急处理, 设备类(fault/offline)按普通.
var eventMeta = map[string]eventMetaT{
	"fire":          {Title: "火灾告警处置", Priority: model.PriorityUrgent},
	"smoke":         {Title: "烟雾告警处置", Priority: model.PriorityUrgent},
	"intrusion":     {Title: "非法入侵告警处置", Priority: model.PriorityUrgent},
	"door_force":    {Title: "门禁强开告警处置", Priority: model.PriorityUrgent},
	"fault":         {Title: "设备故障报修", Priority: model.PriorityNormal},
	"offline_alert": {Title: "设备离线排查", Priority: model.PriorityNormal},
}

// DecodeAlarm 解析一条告警消息.
// 非法消息返回 error, 由调用方记录后跳过(不能返回给消费循环, 否则位移不提交会卡住分区).
func DecodeAlarm(value []byte) (AlarmEvent, error) {
	var evt AlarmEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		return AlarmEvent{}, fmt.Errorf("解析告警消息失败: %w", err)
	}
	if evt.RequestID == "" {
		return AlarmEvent{}, errors.New("告警消息缺少 request_id, 无法做幂等去重")
	}
	if evt.DeviceID == "" {
		return AlarmEvent{}, errors.New("告警消息缺少 device_id")
	}
	return evt, nil
}

// RepairOrderDraft 由告警事件推导出的报修工单草稿(纯函数, 便于单测).
type RepairOrderDraft struct {
	AlarmID     string // 幂等键, 取 request_id, 落 work_order.alarm_id
	TenantID    int64  // 园区ID: 优先取 payload.tenant_id, 缺省用配置的兜底租户
	Type        int8   // 工单类型, 固定报修
	Title       string
	Priority    int8
	Location    string // 取 payload.zone_code / location, 提取不到留空
	Description string
}

// payloadMeta 告警 payload 中本服务关心的字段.
type payloadMeta struct {
	TenantID int64  `json:"tenant_id"` // 事件携带的园区ID(可选)
	ZoneCode string `json:"zone_code"` // 区域编码(可选)
	Location string `json:"location"`  // 位置描述(可选)
}

// BuildRepairDraft 把告警事件映射成报修工单草稿.
//
// location 取值顺序: payload.zone_code -> payload.location -> 空, 提取不到就留空由人工补充
// —— 不臆造位置, 编造的位置会把维修人员指到错误的地点.
func BuildRepairDraft(evt AlarmEvent, defaultTenantID int64) RepairOrderDraft {
	meta, ok := eventMeta[evt.EventType]
	if !ok {
		// 未知告警类型按普通优先级处理, 不丢弃 —— 丢弃会漏掉真实告警.
		meta = eventMetaT{
			Title:    fmt.Sprintf("设备告警处置(%s)", evt.EventType),
			Priority: model.PriorityNormal,
		}
	}

	var p payloadMeta
	if len(evt.Payload) > 0 {
		_ = json.Unmarshal(evt.Payload, &p) // payload 解析失败不致命, 后面字段取零值
	}

	tenantID := p.TenantID
	if tenantID <= 0 {
		tenantID = defaultTenantID
	}

	return RepairOrderDraft{
		AlarmID:  evt.RequestID,
		TenantID: tenantID,
		Type:     model.WorkOrderTypeRepair,
		Title:    meta.Title,
		Priority: meta.Priority,
		Location: firstNonEmpty(p.ZoneCode, p.Location),
		Description: fmt.Sprintf("设备 %s(%s) 触发 %s (event_type=%s), 发生于 %d, 需到场处置。",
			evt.DeviceID, evt.DeviceType, meta.Title, evt.EventType, evt.OccurredAt),
	}
}

// firstNonEmpty 返回第一个非空字符串.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
