package frame

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"onepark/app/gateway-service/internal/model"
	"onepark/app/gateway-service/internal/svc"
	"onepark/common/kafka"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
)

// Session 与传输无关的帧处理会话: 承载设备身份状态(authed/deviceID/tenantID/zoneID)
// 与业务动作(auth/publish/finishCommand). TCP 与 CoAP 各自只负责"字节↔Frame"的编解码与
// 连接生命周期, 业务语义全部收敛到 HandleFrame/HandleStateless, 保证双协议行为一致.
//
// 实例语义差异: TCP 下为每连接实例(authed 跨该连接多帧复用, 见 HandleFrame);
// CoAP 下为每请求实例(无连接, 每请求重新认证, 见 HandleStateless).
type Session struct {
	logx.Logger
	svcCtx   *svc.ServiceContext
	source   string // 消息来源标识: tcp-gateway / coap-gateway
	remote   string // 对端地址, 仅用于日志; TCP 建连时设置, CoAP 每请求按 peer 设置
	deviceID string
	tenantID int64
	zoneID   string
	authed   bool
}

// NewSession 创建 TCP 默认会话(来源 tcp-gateway). CoAP 侧需另行 SetSource/SetRemote.
func NewSession(svcCtx *svc.ServiceContext) *Session {
	return &Session{Logger: logx.WithContext(context.Background()), svcCtx: svcCtx, source: "tcp-gateway"}
}

// SetRemote 设置对端地址(用于日志).
func (s *Session) SetRemote(remote string) { s.remote = remote }

// SetSource 设置消息来源标识(CoAP 侧置 coap-gateway, 区分上行来源便于对账).
func (s *Session) SetSource(src string) { s.source = src }

// HandleFrame TCP 长连接模式: 首帧必须 auth 一次置 authed, 后续帧复用连接状态直接分发.
// 返回 keep=false 时 TCP 长连接应断开.
func (s *Session) HandleFrame(ctx context.Context, f *Frame) (Response, bool) {
	if !s.authed {
		if f.Type != TypeAuth {
			return Response{OK: false, Error: "未认证, 请先发送 auth 帧"}, false
		}
		return s.auth(ctx, f.DeviceID, f.Secret)
	}
	return s.dispatch(ctx, f)
}

// HandleStateless CoAP 无连接模式: 每请求携带 device_id+secret, 先认证再按类型分发
// (设计文档 §4.1 选项 A). CoAP 无连接概念, authed 不跨请求保留, 故每请求重认证.
func (s *Session) HandleStateless(ctx context.Context, f *Frame) (Response, bool) {
	if f.DeviceID == "" || f.Secret == "" {
		return Response{OK: false, Error: "device_id 与 secret 不能为空"}, true
	}
	if resp, ok := s.auth(ctx, f.DeviceID, f.Secret); !ok {
		return resp, true
	}
	if f.Type == TypeAuth {
		return Response{OK: true, Type: TypeAuth}, true
	}
	return s.dispatch(ctx, f)
}

// auth 设备认证: 校验设备存在且密钥匹配(bcrypt), 成功后置在线.
// 供 TCP auth 帧与 CoAP 每请求认证复用(见 §4.1 选项 A/C 说明).
func (s *Session) auth(ctx context.Context, deviceID, secret string) (Response, bool) {
	if deviceID == "" || secret == "" {
		return Response{OK: false, Type: TypeAuth, Error: "device_id 与 secret 不能为空"}, false
	}

	device, err := s.svcCtx.DeviceModel.FindByDeviceID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.Errorf("设备不存在, 断开: remote=%s, deviceId=%s", s.remote, deviceID)
			return Response{OK: false, Type: TypeAuth, Error: "设备不存在"}, false
		}
		s.Errorf("查询设备失败: deviceId=%s, err=%v", deviceID, err)
		return Response{OK: false, Type: TypeAuth, Error: "认证服务异常"}, false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(device.DeviceSecret), []byte(secret)); err != nil {
		s.Errorf("设备密钥校验失败, 断开: remote=%s, deviceId=%s", s.remote, deviceID)
		return Response{OK: false, Type: TypeAuth, Error: "密钥校验失败"}, false
	}

	s.authed = true
	s.deviceID = device.DeviceID
	s.tenantID = device.TenantID
	s.zoneID = device.ZoneID

	if err := s.svcCtx.DeviceModel.UpdateOnline(ctx, device.DeviceID, model.DeviceStatusOnline, time.Now()); err != nil {
		s.Errorf("设备上线状态回写失败: deviceId=%s, err=%v", device.DeviceID, err)
	}

	s.Infof("设备认证成功: deviceId=%s, productKey=%s, remote=%s", device.DeviceID, device.ProductKey, s.remote)
	return Response{OK: true, Type: TypeAuth}, true
}

// dispatch 按帧类型分发业务(需已认证). 与传输无关, TCP/CoAP 共用.
func (s *Session) dispatch(ctx context.Context, f *Frame) (Response, bool) {
	switch f.Type {
	case TypePing:
		return Response{OK: true, Type: TypePing}, true

	case TypeTelemetry:
		if len(f.Metrics) == 0 {
			return Response{OK: false, Type: TypeTelemetry, Error: "metrics 不能为空"}, true
		}
		if err := s.publish(ctx, f, TypeTelemetry, map[string]any{"metrics": f.Metrics}); err != nil {
			s.Errorf("遥测投递失败: deviceId=%s, err=%v", s.deviceID, err)
			return Response{OK: false, Type: TypeTelemetry, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeTelemetry}, true

	case TypeEvent:
		if f.EventType == "" {
			return Response{OK: false, Type: TypeEvent, Error: "event_type 不能为空"}, true
		}
		if err := s.publish(ctx, f, f.EventType, f.Payload); err != nil {
			s.Errorf("事件投递失败: deviceId=%s, err=%v", s.deviceID, err)
			return Response{OK: false, Type: TypeEvent, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeEvent}, true

	case TypeStatus:
		if f.Status == "" {
			return Response{OK: false, Type: TypeStatus, Error: "status 不能为空"}, true
		}
		if err := s.publish(ctx, f, TypeStatus, map[string]any{"status": f.Status}); err != nil {
			s.Errorf("状态投递失败: deviceId=%s, err=%v", s.deviceID, err)
			return Response{OK: false, Type: TypeStatus, Error: "投递失败"}, true
		}
		return Response{OK: true, Type: TypeStatus}, true

	case TypeAck:
		if f.RequestID == "" {
			return Response{OK: false, Type: TypeAck, Error: "request_id 不能为空"}, true
		}
		if err := s.finishCommand(ctx, f); err != nil {
			s.Errorf("指令回执处理失败: requestId=%s, err=%v", f.RequestID, err)
			return Response{OK: false, Type: TypeAck, Error: "回执处理失败"}, true
		}
		return Response{OK: true, Type: TypeAck}, true

	default:
		return Response{OK: false, Error: "未知帧类型: " + f.Type}, true
	}
}

// publish 投递上报消息到遥测 topic; 告警类事件额外投递告警 topic 供 M3 消费.
func (s *Session) publish(ctx context.Context, f *Frame, eventType string, payload any) error {
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
		TenantID:   s.tenantID,
		DeviceID:   s.deviceID,
		DeviceType: f.DeviceType,
		EventType:  eventType,
		ZoneID:     s.zoneID,
		OccurredAt: occurredAt,
		Payload:    raw,
		Source:     s.source,
	}
	value, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	if err := s.svcCtx.Producer.Publish(ctx, kafka.TopicDeviceTelemetry, []byte(s.deviceID), value); err != nil {
		return err
	}
	if kafka.IsAlarmEvent(eventType) {
		if err := s.svcCtx.Producer.Publish(ctx, kafka.TopicAlarm, []byte(s.deviceID), value); err != nil {
			s.Errorf("告警事件投递失败: deviceId=%s, requestId=%s, err=%v", s.deviceID, requestID, err)
		}
	}
	return nil
}

// finishCommand 处理指令回执, 回写 command_log.
func (s *Session) finishCommand(ctx context.Context, f *Frame) error {
	status := model.CommandStatusSuccess
	if f.Status == "failed" || f.Status == "error" {
		status = model.CommandStatusFailed
	}
	response, err := json.Marshal(f.Payload)
	if err != nil {
		response = []byte(`{}`)
	}
	return s.svcCtx.CommandLogModel.Finish(ctx, f.RequestID, status, response)
}
