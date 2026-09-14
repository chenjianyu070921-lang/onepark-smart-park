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
type deviceTelemetry struct {
	TenantID    int64  `json:"tenant_id"`    // 园区ID(RBAC 隔离)
	DeviceID    string `json:"device_id"`    // 地磁/门禁设备ID
	Event       string `json:"event"`        // entry 入场 / exit 离场
	PlateNo     string `json:"plate_no"`     // 车牌号
	VehicleType int8   `json:"vehicle_type"` // 1月卡 2临时 3VIP 4异常
	Timestamp   int64  `json:"timestamp"`    // 事件时间(秒级时间戳)
}

// handleTelemetry 处理一条地磁遥测消息: 入场建记录, 离场计费并更新, 异常发布告警.
func (s *ServiceContext) handleTelemetry(ctx context.Context, msg kafkago.Message) error {
	var t deviceTelemetry
	if err := json.Unmarshal(msg.Value, &t); err != nil {
		// 消息格式非法, 记录后跳过(不阻塞后续消费).
		fmt.Printf("[warn] parking invalid telemetry: %v\n", err)
		return nil
	}

	switch t.Event {
	case "entry":
		return s.onTelemetryEntry(ctx, &t)
	case "exit":
		return s.onTelemetryExit(ctx, &t)
	default:
		fmt.Printf("[warn] parking unknown telemetry event=%s\n", t.Event)
		return nil
	}
}

// onTelemetryEntry 入场: 新建停车记录(停车中).
func (s *ServiceContext) onTelemetryEntry(ctx context.Context, t *deviceTelemetry) error {
	now := time.Now()
	rec := &model.ParkingRecord{
		PlateNo:     t.PlateNo,
		VehicleType: t.VehicleType,
		DeviceIDIn:  t.DeviceID,
		EntryTime:   &now,
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

	now := time.Now()
	dur := 0
	if rec.EntryTime != nil {
		dur = int(now.Sub(*rec.EntryTime).Minutes())
	}
	fee := CalcFee(rec.EntryTime, &now, rec.VehicleType)

	updates := map[string]interface{}{
		"status":         model.ParkingStatusDone,
		"exit_time":      now,
		"duration_min":   dur,
		"fee":            fee,
		"device_id_out":  t.DeviceID,
		"updated_at":     now,
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
