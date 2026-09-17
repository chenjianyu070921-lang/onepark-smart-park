// Package consumer 消费共享 Kafka 上的告警事件, 自动生成调度工单(接口清单 #74).
//
// 数据源: M1 event-dispatcher 投递到 `alarm-event` 的告警消息(见 common/kafka/topics.go)。
// 使用纪律: 共享 broker 为多项目共用, 只读 onepark 体系的 topic, 消费组需带项目+人+环境,
// 且消费者默认关闭 —— 详见 docs/plans/2026-09-15-M5-环境与方案确认书.md §1.5。
package consumer

import (
	"encoding/json"
	"errors"
	"fmt"

	"onepark/app/dispatch-service/internal/model"
)

// 技能标签取值。与 dispatch_staff.skills 中的写法保持一致(均为小写)。
// 这三个值同时出现在告警映射与人工建单入口, 收敛到常量避免拼写漂移。
const (
	skillFire       = "fire"       // 消防
	skillSecurity   = "security"   // 安防
	skillElectrical = "electrical" // 强弱电/设备
)

// AlarmEvent 是 M1 event-dispatcher 投递到 alarm-event 的告警消息体。
//
// 字段与 app/event-dispatcher/internal/dispatch.Message 对齐。
// ⚠️ 该结构目前由 M1 单方面定义、尚未下沉到 common 包, 若 M1 调整字段需同步本文件。
type AlarmEvent struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"`
}

// eventMetaT 告警类型对应的工单标题、优先级与所需技能。
// 优先级取值与 dispatch_task.priority 一致: 1 紧急 / 2 高 / 3 普通。
type eventMetaT struct {
	Title    string
	Priority int8
	// Skill 是处理该类告警所需的技能标签, 供自动指派按技能选人;
	// 空字符串表示不限技能(算法自动退化为就近+负载)。
	Skill string
}

// eventMeta 告警类型映射表。事件类型取值与 M1 event-dispatcher 的 alarmEventTypes 对齐。
var eventMeta = map[string]eventMetaT{
	"fire":          {Title: "火灾告警", Priority: model.PriorityUrgent, Skill: skillFire},
	"smoke":         {Title: "烟雾告警", Priority: model.PriorityUrgent, Skill: skillFire},
	"intrusion":     {Title: "非法入侵告警", Priority: model.PriorityHigh, Skill: skillSecurity},
	"door_force":    {Title: "门禁强开告警", Priority: model.PriorityHigh, Skill: skillSecurity},
	"fault":         {Title: "设备故障告警", Priority: model.PriorityNormal, Skill: skillElectrical},
	"offline_alert": {Title: "设备离线告警", Priority: model.PriorityNormal, Skill: skillElectrical},
}

// DecodeAlarm 解析一条告警消息。
// 非法消息返回 error, 由调用方记录后跳过(不能返回给消费循环, 否则位移不提交会卡住分区)。
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

// TaskDraft 由告警事件推导出的调度工单草稿。
type TaskDraft struct {
	AlarmID       string // 幂等键, 取 request_id
	Title         string
	ZoneCode      string
	RequiredSkill string // 所需技能, 供自动指派按技能选人
	Priority      int8
	Description   string
}

// payloadZone 告警 payload 中可能携带的位置字段。
type payloadZone struct {
	ZoneCode string `json:"zone_code"`
	Location string `json:"location"`
}

// BuildTaskDraft 把告警事件映射成调度工单草稿(纯函数, 便于单测)。
//
// zone_code 取值顺序: payload.zone_code -> payload.location -> 空。
// 拿不到就留空, 工单落到「待指派」由人工指派 —— **不臆造区域**,
// 因为就近指派依赖真实的区域编码, 编造出来的区域会把人派到错误的地方。
func BuildTaskDraft(evt AlarmEvent) TaskDraft {
	meta, ok := eventMeta[evt.EventType]
	if !ok {
		// 未知告警类型按普通优先级处理, 不丢弃 —— 丢弃会漏掉真实告警。
		meta = eventMetaT{
			Title:    fmt.Sprintf("设备告警(%s)", evt.EventType),
			Priority: model.PriorityNormal,
		}
	}

	return TaskDraft{
		AlarmID:       evt.RequestID,
		Title:         meta.Title,
		ZoneCode:      extractZone(evt.Payload),
		RequiredSkill: meta.Skill,
		Priority:      meta.Priority,
		Description:   fmt.Sprintf("设备 %s 触发 %s (event_type=%s), 需现场处置", evt.DeviceID, meta.Title, evt.EventType),
	}
}

// extractZone 从 payload 中尽力提取区域编码; 提取不到返回空串。
func extractZone(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p payloadZone
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	if p.ZoneCode != "" {
		return p.ZoneCode
	}
	return p.Location
}
