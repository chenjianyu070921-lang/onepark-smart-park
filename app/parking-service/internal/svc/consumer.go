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

	"onepark/app/parking-service/internal/model"
	"onepark/common/kafka"

	kafkago "github.com/segmentio/kafka-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// keyDedup 消费幂等键前缀, 完整键形如 parking:dedup:{幂等ID}.
const keyDedup = "parking:dedup:"

// deviceTelemetry M1 设备遥测消息(地磁/门禁上报), 由 parking-service 消费驱动停车记录.
// 信封字段与三个生产者(event-dispatcher MQTT / device-service http-fallback /
// gateway-service tcp-gateway)的 Message 结构保持一致 —— P0-2 兼容性验证结论(2026-09-17):
// 旧扁平格式(event/plate_no/timestamp 在顶层)对真实链路完全不兼容, 已按标准信封适配.
type deviceTelemetry struct {
	// 标准信封(与 common 事实标准一致)
	RequestID  string          `json:"request_id"`  // 幂等键(M1 侧生成); 缺失时按指纹降级
	DeviceID   string          `json:"device_id"`   // 地磁/门禁设备ID
	EventType  string          `json:"event_type"`  // entry 入场 / exit 离场
	OccurredAt int64           `json:"occurred_at"` // 事件时间(秒级时间戳)
	Payload    json.RawMessage `json:"payload"`     // 业务载荷, 停车字段约定在其中
	Source     string          `json:"source"`      // mqtt / http-fallback / tcp-gateway

	// 停车业务字段: 标准链路放在 payload 内; 旧扁平格式(测试直投)在顶层, 解析时合并回退
	TenantID    int64  `json:"tenant_id"`    // 园区ID(RBAC 隔离)
	PlateNo     string `json:"plate_no"`     // 车牌号
	VehicleType int8   `json:"vehicle_type"` // 1月卡 2临时 3VIP 4异常
	Event       string `json:"event"`        // 旧字段名, 兜底回退
	Timestamp   int64  `json:"timestamp"`    // 旧时间字段, 兜底回退
}

// telemetryPayload 标准信封 payload 内的停车业务字段约定(设备侧/联调直投需按此上报).
type telemetryPayload struct {
	TenantID    int64  `json:"tenant_id"`
	PlateNo     string `json:"plate_no"`
	VehicleType int8   `json:"vehicle_type"`
}

// normalizeTelemetry 将原始报文归一化为停车业务可用的遥测事件:
// 兼容标准信封(event_type + payload 业务字段)与旧扁平格式(event/plate_no/timestamp 顶层);
// 事件时间缺省回落到当前时间, 保证 EntryTime 恒有值.
func normalizeTelemetry(raw *deviceTelemetry) *deviceTelemetry {
	t := &deviceTelemetry{
		TenantID:    raw.TenantID,
		DeviceID:    raw.DeviceID,
		Event:       raw.EventType,
		PlateNo:     raw.PlateNo,
		VehicleType: raw.VehicleType,
		Timestamp:   raw.OccurredAt,
	}
	// 旧扁平格式字段兜底.
	if t.Event == "" {
		t.Event = raw.Event
	}
	if t.Timestamp <= 0 {
		t.Timestamp = raw.Timestamp
	}
	// 标准链路的停车业务字段在 payload 内.
	if len(raw.Payload) > 0 {
		var p telemetryPayload
		if e := json.Unmarshal(raw.Payload, &p); e == nil {
			if t.TenantID == 0 {
				t.TenantID = p.TenantID
			}
			if t.PlateNo == "" {
				t.PlateNo = p.PlateNo
			}
			if t.VehicleType == 0 {
				t.VehicleType = p.VehicleType
			}
		}
	}
	if t.Timestamp <= 0 {
		t.Timestamp = time.Now().Unix()
	}
	return t
}

// IdempotentID 返回写入 parking_record.request_id 的幂等键.
// 优先使用消息自带 request_id; 缺失时按 sha1(device|event|plate|timestamp) 生成指纹降级.
// 禁止用 partition-offset 作幂等键: 重放时 offset 变化会导致重复处理.
func (t *deviceTelemetry) IdempotentID() string {
	if r := strings.TrimSpace(t.RequestID); r != "" {
		return r
	}
	raw := strings.Join([]string{t.DeviceID, t.Event, t.PlateNo, strconv.FormatInt(t.Timestamp, 10)}, "|")
	sum := sha1.Sum([]byte(raw))
	return "fp:" + hex.EncodeToString(sum[:])
}

// handleTelemetry 处理一条地磁遥测消息: 入场建记录, 离场计费并更新, 异常发布告警.
//
// 幂等(L1 + L3): Kafka 是 at-least-once, 重平衡/重投会让同一条消息被处理多次。
// 没有幂等时入场会建出多条"停车中"记录, 离场会重复计费并重复广播事件。
func (s *ServiceContext) handleTelemetry(ctx context.Context, msg kafkago.Message) error {
	var raw deviceTelemetry
	if err := json.Unmarshal(msg.Value, &raw); err != nil {
		// 坏消息: 重试多少次结果都一样, 只能记日志跳过(返回 nil 提交位移),
		// 否则单条坏消息会卡死整个分区(毒丸). parking 没有死信台账, 坏消息不可重放.
		fmt.Printf("[error] parking malformed telemetry offset=%d err=%v payload=%s\n", msg.Offset, err, msg.Value)
		return nil
	}
	t := normalizeTelemetry(&raw)

	if t.TenantID == 0 {
		fmt.Printf("[warn] parking telemetry missing tenant_id: device=%s event=%s\n", t.DeviceID, t.Event)
	}

	switch t.Event {
	case "entry":
		return s.onTelemetryEntry(ctx, t)
	case "exit":
		return s.onTelemetryExit(ctx, t)
	default:
		fmt.Printf("[warn] parking unknown telemetry event=%s\n", t.Event)
		return nil
	}
}

// seen 判定该消息是否已处理过(L1).
// 幂等组件不可用(Redis 故障)时返回 error, 由消费端重投 —— 放行一条重复消息会导致重复计费,
// 代价远大于短暂积压, 与 alarm-service 的策略保持一致.
func (s *ServiceContext) seen(ctx context.Context, t *deviceTelemetry) (bool, error) {
	if s.Dedup == nil {
		// 未配置 Redis 时无法去重: 交给 L3 唯一索引兜底, 不阻断消费.
		return false, nil
	}
	id := t.IdempotentID()
	seen, err := s.Dedup.Seen(ctx, keyDedup+id)
	if err != nil {
		return false, fmt.Errorf("parking dedup unavailable request_id=%s: %w", id, err)
	}
	return seen, nil
}

// onTelemetryEntry 入场: 新建停车记录(停车中).
func (s *ServiceContext) onTelemetryEntry(ctx context.Context, t *deviceTelemetry) error {
	id := t.IdempotentID()
	seen, err := s.seen(ctx, t)
	if err != nil {
		return err
	}
	if seen {
		fmt.Printf("[info] parking skip duplicated entry request_id=%s plate=%s\n", id, t.PlateNo)
		return nil
	}

	eventTime := time.Unix(t.Timestamp, 0) // 事件时间(occurred_at), 而非处理时间
	now := time.Now()
	rec := &model.ParkingRecord{
		PlateNo:     t.PlateNo,
		VehicleType: t.VehicleType,
		DeviceIDIn:  t.DeviceID,
		EntryTime:   &eventTime,
		Status:      model.ParkingStatusParking,
		RequestID:   &id,
	}
	rec.TenantID = t.TenantID
	rec.CreatedAt = now
	rec.UpdatedAt = now

	// L3 兜底: Redis 键过期但库里已有(或 L1 不可用)时, 唯一索引是最后一道防线.
	// 用 OnConflict{DoNothing} 而不是"捕获 1062 再转译": 前者靠 RowsAffected 判定,
	// 无需解析 MySQL 错误码, 也不必为此引入 go-sql-driver 依赖.
	res := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(rec)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		fmt.Printf("[info] parking skip duplicated insert request_id=%s plate=%s\n", id, t.PlateNo)
		return nil
	}
	// 发布车辆入场事件, 供大屏/告警订阅.
	s.publish(ctx, kafka.TopicParkingEntry, t.PlateNo, msgOf(rec))
	return nil
}

// onTelemetryExit 离场: 更新最近一条停车中记录, 计算时长与费用并置已完成.
func (s *ServiceContext) onTelemetryExit(ctx context.Context, t *deviceTelemetry) error {
	id := t.IdempotentID()
	seen, err := s.seen(ctx, t)
	if err != nil {
		return err
	}
	if seen {
		fmt.Printf("[info] parking skip duplicated exit request_id=%s plate=%s\n", id, t.PlateNo)
		return nil
	}

	var rec model.ParkingRecord
	if err := s.DB.WithContext(ctx).
		Where("plate_no=? AND tenant_id=? AND status=?", t.PlateNo, t.TenantID, model.ParkingStatusParking).
		Order("id DESC").First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[warn] parking exit but no active record plate=%s\n", t.PlateNo)
			return nil
		}
		return err
	}

	exitTime := time.Unix(t.Timestamp, 0) // 事件时间(occurred_at), 时长/计费均按事件口径
	now := time.Now()
	dur := 0
	if rec.EntryTime != nil {
		dur = int(exitTime.Sub(*rec.EntryTime).Minutes())
	}
	fee := CalcFee(rec.EntryTime, &exitTime, rec.VehicleType)

	// CAS: 更新条件必须带上 status=停车中. 只按 id 更新时, 两条并发的离场消息
	// 会各自算一次费用并各广播一次离场事件 —— 重复计费比"少算一次"难查得多.
	res := s.DB.WithContext(ctx).Model(&model.ParkingRecord{}).
		Where("id=? AND tenant_id=? AND status=?", rec.ID, t.TenantID, model.ParkingStatusParking).
		Updates(map[string]interface{}{
			"status":        model.ParkingStatusDone,
			"exit_time":     exitTime,
			"duration_min":  dur,
			"fee":           fee,
			"device_id_out": t.DeviceID,
			"updated_at":    now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 已被另一条离场消息结算(或人工结单): 本次不再重复计费与广播.
		fmt.Printf("[info] parking exit already settled, skip request_id=%s plate=%s\n", id, t.PlateNo)
		return nil
	}
	rec.Status = model.ParkingStatusDone
	rec.ExitTime = &exitTime
	rec.DurationMin = &dur
	rec.Fee = fee

	// 发布车辆离场事件; 异常车辆额外发布告警事件(供 M3 安防联动).
	s.publish(ctx, kafka.TopicParkingExit, t.PlateNo, msgOf(&rec))
	if rec.VehicleType == model.VehicleTypeAbnormal {
		s.publish(ctx, kafka.TopicAlarm, t.PlateNo, alarmMsg(t))
	}
	return nil
}

// publish 通过生产者发布事件(生产者未初始化时降级为日志).
func (s *ServiceContext) publish(ctx context.Context, topic, key string, value []byte) {
	if s.Producer == nil {
		fmt.Printf("[warn] parking producer nil, skip publish topic=%s\n", topic)
		return
	}
	if err := s.Producer.Publish(ctx, topic, []byte(key), value); err != nil {
		fmt.Printf("[error] parking publish topic=%s failed: %v\n", topic, err)
	}
}

// CalcFee 简化计费: 月卡/VIP 免费, 临时车首 15 分钟免费, 之后 5 元/小时向上取整.
func CalcFee(entry, exit *time.Time, vehicleType int8) float64 {
	switch vehicleType {
	case model.VehicleTypeMonthly, model.VehicleTypeVIP:
		return 0
	}
	if entry == nil || exit == nil {
		return 0
	}
	mins := int(exit.Sub(*entry).Minutes())
	if mins <= 15 {
		return 0
	}
	hours := (mins + 59) / 60 // 向上取整到小时
	return float64(hours) * 5.0
}

// msgOf 将停车记录序列化为事件消息体.
func msgOf(rec *model.ParkingRecord) []byte {
	b, _ := json.Marshal(rec)
	return b
}

// alarmMsg 生成异常车辆告警事件消息体(对齐 common.AlarmEvent 关键字段).
func alarmMsg(t *deviceTelemetry) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"device_id": t.DeviceID,
		"severity":  2,
		"content":   fmt.Sprintf("异常车辆 %s 离场", t.PlateNo),
		"timestamp": t.Timestamp,
	})
	return b
}
