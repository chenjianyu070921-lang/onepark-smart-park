// Package mq 消费 device-telemetry, 完成三件事:
//  1. 上下线报文 -> 回写 device.status / last_online_at(解决设备恒为离线导致指令被拒绝的死锁)
//  2. 遥测指标   -> 写入 TDengine 时序库(供 M4 能源与 M5 大屏取历史曲线)
//  3. 遥测指标   -> 合并进设备影子 reported(让影子不再是空壳)
package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/common/kafka"
	"onepark/common/tdengine"

	"github.com/zeromicro/go-zero/core/logx"
)

// Message 统一契约别名: 消费端与三处生产端共用 common/kafka 的权威定义.
type Message = kafka.DeviceTelemetry

// 事件类型别名(沿用本包内既有引用, 权威定义在 common/kafka).
const (
	EventOnline  = kafka.EventOnline
	EventOffline = kafka.EventOffline
	EventFault   = kafka.EventFault
	EventStatus  = kafka.EventStatus
)

// payload 中的上下线字段约定(二选一): {"online":true} 或 {"status":"online"}
type statusPayload struct {
	Online *bool  `json:"online"`
	Status string `json:"status"`
}

// metricsPayload 遥测上报约定: {"metrics":{"temperature":23.5,"humidity":60}}
type metricsPayload struct {
	Metrics map[string]any `json:"metrics"`
}

// Handler 遥测消费处理器.
type Handler struct {
	logx.Logger
	svcCtx *svc.ServiceContext
}

func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{Logger: logx.WithContext(context.Background()), svcCtx: svcCtx}
}

// Handle 处理一条遥测消息; 返回 nil 表示成功(提交位移).
// 业务上的"无需处理"同样返回 nil, 避免坏消息反复重投阻塞消费.
func (h *Handler) Handle(ctx context.Context, msg kafka.Message) error {
	var m Message
	if err := json.Unmarshal(msg.Value, &m); err != nil {
		h.Errorf("遥测消息解析失败, 丢弃: err=%v", err)
		return nil
	}
	if m.DeviceID == "" {
		h.Errorf("遥测消息缺少 device_id, 丢弃: topic=%s", msg.Topic)
		return nil
	}

	if isStatusEvent(m) {
		return h.handleStatus(ctx, &m)
	}
	return h.handleTelemetry(ctx, &m)
}

// Consume 启动消费循环, 阻塞运行; ctx 取消即退出.
func (h *Handler) Consume(ctx context.Context, brokers string) error {
	consumer := kafka.NewConsumer(brokers, kafka.TopicDeviceTelemetry, kafka.GroupDevice)
	defer consumer.Close()

	logx.Infof("遥测消费已启动: topic=%s, group=%s", kafka.TopicDeviceTelemetry, kafka.GroupDevice)
	return consumer.Consume(ctx, h.Handle)
}

func isStatusEvent(m Message) bool {
	if m.EventType == EventStatus {
		return true
	}
	var p statusPayload
	if len(m.Payload) > 0 {
		_ = json.Unmarshal(m.Payload, &p)
	}
	if p.Online != nil {
		return true
	}
	switch p.Status {
	case EventOnline, EventOffline, EventFault:
		return true
	}
	return m.EventType == EventOnline || m.EventType == EventOffline
}

// handleStatus 回写设备在线状态.
func (h *Handler) handleStatus(ctx context.Context, m *Message) error {
	status := model.DeviceStatusOffline
	switch {
	case m.EventType == EventFault:
		status = model.DeviceStatusFault
	default:
		var p statusPayload
		if len(m.Payload) > 0 {
			_ = json.Unmarshal(m.Payload, &p)
		}
		if p.Online != nil && *p.Online {
			status = model.DeviceStatusOnline
		}
		if p.Status == EventOnline {
			status = model.DeviceStatusOnline
		}
		if p.Status == EventOffline {
			status = model.DeviceStatusOffline
		}
		if p.Status == EventFault {
			status = model.DeviceStatusFault
		}
	}

	occurredAt := time.Now()
	if m.OccurredAt > 0 {
		occurredAt = time.Unix(m.OccurredAt, 0)
	}

	if err := h.svcCtx.DeviceModel.UpdateOnline(ctx, m.DeviceID, status, occurredAt); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.Errorf("状态回写跳过, 设备不存在: deviceId=%s", m.DeviceID)
			return nil
		}
		h.Errorf("设备状态回写失败: deviceId=%s, err=%v", m.DeviceID, err)
		return err
	}
	h.Infof("设备状态已更新: deviceId=%s, status=%d, source=%s", m.DeviceID, status, m.Source)
	return nil
}

// handleTelemetry 遥测落时序库并回写影子 reported.
func (h *Handler) handleTelemetry(ctx context.Context, m *Message) error {
	var p metricsPayload
	if len(m.Payload) > 0 {
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			h.Errorf("遥测 payload 解析失败, 丢弃: deviceId=%s, err=%v", m.DeviceID, err)
			return nil
		}
	}
	if len(p.Metrics) == 0 {
		// 非遥测类事件(如门磁开关)不写时序库, 也不污染影子
		return nil
	}

	occurredAt := time.Now()
	if m.OccurredAt > 0 {
		occurredAt = time.Unix(m.OccurredAt, 0)
	}

	// 1. 落 TDengine(未配置时自动跳过)
	if h.svcCtx.TDengine != nil {
		for metric, v := range p.Metrics {
			value, ok := toFloat(v)
			if !ok {
				continue
			}
			if err := h.svcCtx.TDengine.WritePoint(ctx, tdengine.Point{
				DeviceID: m.DeviceID,
				Metric:   metric,
				Value:    value,
				Ts:       occurredAt,
			}); err != nil {
				h.Errorf("遥测落时序库失败: deviceId=%s, metric=%s, err=%v", m.DeviceID, metric, err)
			}
		}
	}

	// 2. 合并进影子 reported
	if err := h.mergeReported(ctx, m.DeviceID, p.Metrics); err != nil {
		h.Errorf("影子 reported 回写失败: deviceId=%s, err=%v", m.DeviceID, err)
		return nil // 影子回写失败不阻塞消费, 避免遥测堆积
	}
	return nil
}

// mergeReportedMaxAttempts 影子写入版本冲突的最大重试次数.
const mergeReportedMaxAttempts = 3

// mergeReported 将遥测指标合并进影子 reported 后写入(乐观锁, 冲突重试).
// 语义与 shadow-service gRPC UpdateReported 一致: version 条件更新, 0 行即冲突.
func (h *Handler) mergeReported(ctx context.Context, deviceID string, metrics map[string]any) error {
	for attempt := 0; attempt < mergeReportedMaxAttempts; attempt++ {
		s, err := h.svcCtx.ShadowModel.FindByDeviceID(ctx, deviceID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// 影子缺失(如历史设备), 补建后再走正常路径, 保证上报不丢
			s = &model.Shadow{DeviceID: deviceID, Desired: []byte(`{}`), Reported: []byte(`{}`)}
			if err := h.svcCtx.ShadowModel.Insert(ctx, s); err != nil {
				return err
			}
		}

		reported := map[string]any{}
		if len(s.Reported) > 0 {
			_ = json.Unmarshal(s.Reported, &reported)
		}
		for k, v := range metrics {
			reported[k] = v
		}
		b, err := json.Marshal(reported)
		if err != nil {
			return err
		}

		rows, err := h.svcCtx.ShadowModel.UpdateReported(ctx, deviceID, b, s.Version)
		if err != nil {
			return err
		}
		if rows > 0 {
			return nil
		}
		// 0 行: 并发写入导致版本冲突, 重读快照后重试
		h.Infof("影子写入版本冲突, 重试: deviceId=%s, attempt=%d", deviceID, attempt+1)
	}
	return fmt.Errorf("影子 reported 写入重试耗尽: deviceId=%s", deviceID)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}
