package svc

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"onepark/app/alarm-service/internal/model"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"
)

// M1 设备上报的设备类型 / 事件类型取值 (DeviceEvent v1, 见 docs/m3/10).
const (
	DeviceTypeAccessControl = "access_control" // 门禁设备
	EventTypeIntrusion      = "intrusion"      // 非法闯入
)

// keyDedup L1 幂等键前缀, 完整键形如 alarm:dedup:{幂等ID}, TTL 24h.
const keyDedup = "alarm:dedup:"

// DeviceEvent M1 设备遥测事件, 与 M1 约定的 DeviceEvent v1 结构.
// 字段缺失时的降级策略见 IdempotentID / MatchIntrusionRule 注释.
type DeviceEvent struct {
	RequestID  string          `json:"request_id"`
	TenantID   int64           `json:"tenant_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	AreaID     int64           `json:"area_id"`
	Payload    json.RawMessage `json:"payload"`
	Timestamp  int64           `json:"timestamp"` // 事件时间(毫秒)
}

// IdempotentID 返回写入 alarm.request_id 的幂等键.
// 优先使用消息自带 request_id; 缺失时按 sha1(deviceId|eventType|timestamp) 生成指纹降级,
// 并打 WARN 计数, 以推动 M1 补齐 request_id (P0-2).
// 禁止用 partition-offset 作幂等键: 重放时 offset 变化会导致重复处理.
func (e DeviceEvent) IdempotentID() string {
	if r := strings.TrimSpace(e.RequestID); r != "" {
		return r
	}
	raw := strings.Join([]string{e.DeviceID, e.EventType, strconv.FormatInt(e.Timestamp, 10)}, "|")
	sum := sha1.Sum([]byte(raw))
	return "fp:" + hex.EncodeToString(sum[:])
}

// MatchIntrusionRule 硬编码告警规则: 门禁设备上报非法闯入.
// device_type 缺失时按 event_type 兜底匹配(D1): intrusion 为门禁专属事件, 不会误命中其他设备类型.
// P2 动态规则引擎上线后本函数由 rule.Evaluate 替换.
func (e DeviceEvent) MatchIntrusionRule() bool {
	if e.EventType != EventTypeIntrusion {
		return false
	}
	return e.DeviceType == "" || e.DeviceType == DeviceTypeAccessControl
}

// ToAlarm 将命中的设备事件转换为待落库的告警记录(等级 P2).
func (e DeviceEvent) ToAlarm() *model.Alarm {
	now := time.Now()
	id := e.IdempotentID()
	a := &model.Alarm{
		AlarmNo:   NewAlarmNo(id, now),
		RuleID:    0, // 0 表示硬编码规则(规则引擎上线前)
		DeviceID:  e.DeviceID,
		AreaID:    e.AreaID,
		EventType: e.EventType,
		Level:     model.AlarmLevelMinor,
		Status:    model.AlarmStatusPending,
		Content:   fmt.Sprintf("门禁设备 %s 检测到非法闯入", e.DeviceID),
		RequestID: id,
	}
	// BaseModel 为嵌入字段, Go 不允许在复合字面量中直接赋值提升字段.
	a.TenantID = e.TenantID
	a.CreatedAt = now
	a.UpdatedAt = now
	return a
}

// NewAlarmNo 生成业务唯一的告警编号: AL + yyyymmdd + 幂等键哈希前 8 位.
// 不查库自增序列, 避免额外查询与并发争用; 唯一性由 uk_alarm_no 兜底.
func NewAlarmNo(idempotentID string, at time.Time) string {
	sum := sha1.Sum([]byte(idempotentID))
	return "AL" + at.Format("20060102") + hex.EncodeToString(sum[:])[:8]
}

// HandleDeviceEvent 处理一条设备遥测消息: 解析 -> 规则匹配 -> L1 幂等 -> 落库(L3 唯一索引兜底).
// 返回 nil 才提交 Kafka 位移(at-least-once + 消费端幂等 = 有效 exactly-once).
func (s *ServiceContext) HandleDeviceEvent(ctx context.Context, msg kafkago.Message) error {
	log := logx.WithContext(ctx)

	ev, err := parseDeviceEvent(msg.Value)
	if err != nil {
		// 解析失败属不可重试坏消息: 记录原始报文后提交位移, 避免毒丸卡死整个分区(docs/m3/06 §3).
		log.Errorf("alarm drop malformed device event topic=%s partition=%d offset=%d err=%v payload=%s",
			msg.Topic, msg.Partition, msg.Offset, err, msg.Value)
		return nil
	}
	if !ev.MatchIntrusionRule() {
		return nil
	}
	if s.Alarms == nil || s.Dedup == nil {
		return errors.New("alarm: storage or dedup component not initialized")
	}

	id := ev.IdempotentID()
	if strings.HasPrefix(id, "fp:") {
		// logx 无 Warn 级别, Slowf 即 WARN.
		log.Slowf("alarm device event missing request_id, fallback to fingerprint device_id=%s", ev.DeviceID)
	}

	// L1 消息级幂等: Redis 不可用必须返回 error, 由消费端重投, 不降级放行.
	seen, err := s.Dedup.Seen(ctx, keyDedup+id)
	if err != nil {
		log.Errorf("alarm dedup unavailable, requeue later request_id=%s err=%v", id, err)
		return err
	}
	if seen {
		log.Infof("alarm skip duplicated device event request_id=%s", id)
		return nil
	}

	a := ev.ToAlarm()
	if err := s.Alarms.Create(ctx, a); err != nil {
		if errors.Is(err, model.ErrDuplicateRequest) {
			// L3 唯一索引命中(Redis key 过期但库里已有), 视为已处理.
			log.Infof("alarm skip duplicated insert request_id=%s", id)
			return nil
		}
		log.Errorf("alarm create failed request_id=%s err=%v", id, err)
		return err
	}

	log.Infof("alarm created alarm_no=%s device_id=%s event_type=%s level=%d",
		a.AlarmNo, a.DeviceID, a.EventType, a.Level)
	return nil
}

// parseDeviceEvent 反序列化设备事件并校验必填字段.
func parseDeviceEvent(body []byte) (*DeviceEvent, error) {
	var ev DeviceEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("unmarshal device event: %w", err)
	}
	if ev.DeviceID == "" {
		return nil, errors.New("device event missing device_id")
	}
	if ev.EventType == "" {
		return nil, errors.New("device event missing event_type")
	}
	return &ev, nil
}
