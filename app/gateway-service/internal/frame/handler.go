package frame

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"onepark/app/gateway-service/internal/model"
	"onepark/app/gateway-service/internal/svc"
	"onepark/common/kafka"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
)

// Handler 单条 TCP 连接的会话处理器; 每个连接一个实例, 不共享状态.
type Handler struct {
	logx.Logger
	svcCtx   *svc.ServiceContext
	deviceID string
	authed   bool
}

func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{Logger: logx.WithContext(context.Background()), svcCtx: svcCtx}
}

// Serve 处理一条连接, 直到对端关闭/认证超时/协议错误.
func (h *Handler) Serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	reader := bufio.NewReader(conn)
	cfg := h.svcCtx.Config

	maxBytes := cfg.MaxFrameBytes
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	readTimeout := time.Duration(cfg.ReadTimeoutSec) * time.Second
	if readTimeout <= 0 {
		readTimeout = 120 * time.Second
	}
	authTimeout := time.Duration(cfg.AuthTimeoutSec) * time.Second
	if authTimeout <= 0 {
		authTimeout = 10 * time.Second
	}

	// 建连后必须在认证时限内完成认证
	if err := conn.SetReadDeadline(time.Now().Add(authTimeout)); err != nil {
		h.Errorf("设置读超时失败: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := Read(reader, maxBytes)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				h.Infof("连接空闲超时, 断开: remote=%s", remote)
			} else if !errors.Is(err, net.ErrClosed) && err.Error() != "EOF" {
				h.Errorf("读取帧失败: remote=%s, err=%v", remote, err)
			}
			return
		}
		if len(line) == 0 {
			continue
		}

		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			h.Errorf("帧解析失败, 断开: remote=%s, err=%v", remote, err)
			h.write(conn, Response{OK: false, Error: "帧格式非法"})
			return
		}

		resp, keep := h.handle(ctx, &f, remote)
		h.write(conn, resp)
		if !keep {
			return
		}

		// 认证通过后放宽为普通读超时, 设备以 ping 保活
		if h.authed {
			if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				h.Errorf("设置读超时失败: %v", err)
				return
			}
		}
	}
}

// handle 处理单帧, 返回响应与是否保持连接.
func (h *Handler) handle(ctx context.Context, f *Frame, remote string) (Response, bool) {
	if !h.authed {
		if f.Type != TypeAuth {
			return Response{OK: false, Error: "未认证, 请先发送 auth 帧"}, false
		}
		resp, ok := h.auth(ctx, f, remote)
		return resp, ok
	}

	switch f.Type {
	case TypePing:
		return Response{OK: true, Type: TypePing}, true

	case TypeTelemetry:
		if len(f.Metrics) == 0 {
			return Response{OK: false, Type: TypeTelemetry, Error: "metrics 不能为空"}, true
		}
		if err := h.publish(ctx, f, TypeTelemetry, map[string]any{"metrics": f.Metrics}); err != nil {
			h.Errorf("遥测投递失败: deviceId=%s, err=%v", h.deviceID, err)
			return Response{OK: false, Type: TypeTelemetry, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeTelemetry}, true

	case TypeEvent:
		if f.EventType == "" {
			return Response{OK: false, Type: TypeEvent, Error: "event_type 不能为空"}, true
		}
		if err := h.publish(ctx, f, f.EventType, f.Payload); err != nil {
			h.Errorf("事件投递失败: deviceId=%s, err=%v", h.deviceID, err)
			return Response{OK: false, Type: TypeEvent, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeEvent}, true

	case TypeStatus:
		if f.Status == "" {
			return Response{OK: false, Type: TypeStatus, Error: "status 不能为空"}, true
		}
		if err := h.publish(ctx, f, TypeStatus, map[string]any{"status": f.Status}); err != nil {
			h.Errorf("状态投递失败: deviceId=%s, err=%v", h.deviceID, err)
			return Response{OK: false, Type: TypeStatus, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeStatus}, true

	case TypeAck:
		if f.RequestID == "" {
			return Response{OK: false, Type: TypeAck, Error: "request_id 不能为空"}, true
		}
		if err := h.finishCommand(ctx, f); err != nil {
			h.Errorf("指令回执处理失败: requestId=%s, err=%v", f.RequestID, err)
			return Response{OK: false, Type: TypeAck, Error: "回执处理失败"}, true
		}
		return Response{OK: true, Type: TypeAck}, true

	default:
		return Response{OK: false, Error: "未知帧类型: " + f.Type}, true
	}
}

// auth 设备认证: 校验设备存在且密钥匹配(bcrypt), 成功后置在线.
func (h *Handler) auth(ctx context.Context, f *Frame, remote string) (Response, bool) {
	if f.DeviceID == "" || f.Secret == "" {
		return Response{OK: false, Type: TypeAuth, Error: "device_id 与 secret 不能为空"}, false
	}

	device, err := h.svcCtx.DeviceModel.FindByDeviceID(ctx, f.DeviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.Errorf("设备不存在, 断开: remote=%s, deviceId=%s", remote, f.DeviceID)
			return Response{OK: false, Type: TypeAuth, Error: "设备不存在"}, false
		}
		h.Errorf("查询设备失败: deviceId=%s, err=%v", f.DeviceID, err)
		return Response{OK: false, Type: TypeAuth, Error: "认证服务异常"}, false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(device.DeviceSecret), []byte(f.Secret)); err != nil {
		h.Errorf("设备密钥校验失败, 断开: remote=%s, deviceId=%s", remote, f.DeviceID)
		return Response{OK: false, Type: TypeAuth, Error: "密钥校验失败"}, false
	}

	h.authed = true
	h.deviceID = device.DeviceID

	if err := h.svcCtx.DeviceModel.UpdateOnline(ctx, device.DeviceID, model.DeviceStatusOnline, time.Now()); err != nil {
		h.Errorf("设备上线状态回写失败: deviceId=%s, err=%v", device.DeviceID, err)
	}

	h.Infof("设备认证成功: deviceId=%s, productKey=%s, remote=%s", device.DeviceID, device.ProductKey, remote)
	return Response{OK: true, Type: TypeAuth}, true
}

// publish 投递上报消息到遥测 topic; 告警类事件额外投递告警 topic 供 M3 消费.
func (h *Handler) publish(ctx context.Context, f *Frame, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if raw == nil {
		raw = []byte(`{}`)
	}

	requestID := f.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	occurredAt := f.OccurredAt
	if occurredAt <= 0 {
		occurredAt = time.Now().Unix()
	}

	msg := Message{
		RequestID:  requestID,
		DeviceID:   h.deviceID,
		DeviceType: f.DeviceType,
		EventType:  eventType,
		OccurredAt: occurredAt,
		Payload:    raw,
		Source:     "tcp-gateway",
	}
	value, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	if err := h.svcCtx.Producer.Publish(ctx, kafka.TopicDeviceTelemetry, []byte(h.deviceID), value); err != nil {
		return err
	}
	if _, ok := alarmEventTypes[eventType]; ok {
		if err := h.svcCtx.Producer.Publish(ctx, kafka.TopicAlarm, []byte(h.deviceID), value); err != nil {
			h.Errorf("告警事件投递失败: deviceId=%s, requestId=%s, err=%v", h.deviceID, requestID, err)
		}
	}
	return nil
}

// finishCommand 处理指令回执, 回写 command_log.
func (h *Handler) finishCommand(ctx context.Context, f *Frame) error {
	status := model.CommandStatusSuccess
	if f.Status == "failed" || f.Status == "error" {
		status = model.CommandStatusFailed
	}
	response, err := json.Marshal(f.Payload)
	if err != nil {
		response = []byte(`{}`)
	}
	return h.svcCtx.CommandLogModel.Finish(ctx, f.RequestID, status, response)
}

func (h *Handler) write(conn net.Conn, resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		h.Errorf("写入响应失败: deviceId=%s, err=%v", h.deviceID, err)
	}
}
