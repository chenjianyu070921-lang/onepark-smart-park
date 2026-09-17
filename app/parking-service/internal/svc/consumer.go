package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/common/kafka"

	kafkago "github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

// deviceTelemetry M1 设备遥测消息(地磁/门禁上报), 由 parking-service 消费驱动停车记录.
// 信封字段与三个生产者(event-dispatcher MQTT / device-service http-fallback /
// gateway-service tcp-gateway)的 Message 结构保持一致 —— P0-2 兼容性验证结论(2026-09-17):
// 旧扁平格式(event/plate_no/timestamp 在顶层)对真实链路完全不兼容, 已按标准信封适配.
type deviceTelemetry struct {
	// 标准信封(与 common 事实标准一致)
	RequestID  string          `json:"request_id"`
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

// handleTelemetry 处理一条地磁遥测消息: 入场建记录, 离场计费并更新, 异常发布告警.
func (s *ServiceContext) handleTelemetry(ctx context.Context, msg kafkago.Message) error {
	var raw deviceTelemetry
	if err := json.Unmarshal(msg.Value, &raw); err != nil {
		// 消息格式非法, 记录后跳过(不阻塞后续消费).
		fmt.Printf("[warn] parking invalid telemetry: %v\n", err)
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

// onTelemetryEntry 入场: 新建停车记录(停车中).
func (s *ServiceContext) onTelemetryEntry(ctx context.Context, t *deviceTelemetry) error {
	eventTime := time.Unix(t.Timestamp, 0) // 事件时间(occurred_at), 而非处理时间
	now := time.Now()
	rec := &model.ParkingRecord{
		PlateNo:     t.PlateNo,
		VehicleType: t.VehicleType,
		DeviceIDIn:  t.DeviceID,
		EntryTime:   &eventTime,
		Status:      model.ParkingStatusParking,
	}
	rec.TenantID = t.TenantID
	rec.CreatedAt = now
	rec.UpdatedAt = now
	if err := s.DB.WithContext(ctx).Create(rec).Error; err != nil {
		return err
	}
	// 发布车辆入场事件, 供大屏/告警订阅.
	s.publish(ctx, kafka.TopicParkingEntry, t.PlateNo, msgOf(rec))
	return nil
}

// onTelemetryExit 离场: 更新最近一条停车中记录, 计算时长与费用并置已完成.
func (s *ServiceContext) onTelemetryExit(ctx context.Context, t *deviceTelemetry) error {
	var rec model.ParkingRecord
	if err := s.DB.WithContext(ctx).
		Where("plate_no=? AND tenant_id=? AND status=?", t.PlateNo, t.TenantID, model.ParkingStatusParking).
		Order("id DESC").First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
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

	updates := map[string]interface{}{
		"status":        model.ParkingStatusDone,
		"exit_time":     exitTime,
		"duration_min":  dur,
		"fee":           fee,
		"device_id_out": t.DeviceID,
		"updated_at":    now,
	}
	if err := s.DB.WithContext(ctx).Model(&rec).Updates(updates).Error; err != nil {
		return err
	}

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
