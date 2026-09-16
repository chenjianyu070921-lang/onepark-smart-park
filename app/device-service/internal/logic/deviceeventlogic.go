package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// alarmEventTypes 需要额外投递到告警 topic 的事件类型, M3 alarm-service 消费.
var alarmEventTypes = map[string]struct{}{
	"intrusion":     {}, // 非法入侵
	"fire":          {}, // 火情
	"smoke":         {}, // 烟感
	"fault":         {}, // 设备故障
	"door_force":    {}, // 门禁强开
	"offline_alert": {}, // 异常离线
}

// deviceEventMessage 投递到 Kafka 的设备事件消息体, 与 M3 消费端约定.
type deviceEventMessage struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"` // http-fallback: 不经 EMQX 的降级通道
}

type DeviceEventLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceEventLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceEventLogic {
	return &DeviceEventLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// DeviceEvent 设备事件上报.
// EMQX/event-dispatcher 不可用时的降级通道: 校验设备与参数后直接投递 Kafka,
// 使 M3 告警服务不依赖 MQTT 也能拿到事件源.
func (l *DeviceEventLogic) DeviceEvent(req *types.DeviceEventReq) (resp *types.DeviceEventResp, err error) {
	// 1. 参数校验
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.EventType = strings.TrimSpace(req.EventType)
	if req.DeviceID == "" {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "deviceId 不能为空")
	}
	if req.EventType == "" {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "eventType 不能为空")
	}

	rawPayload := strings.TrimSpace(req.Payload)
	if rawPayload == "" {
		rawPayload = "{}"
	}
	if !json.Valid([]byte(rawPayload)) {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "payload 必须是合法 JSON 字符串")
	}

	// 2. 设备存在性校验
	if _, err := l.svcCtx.DeviceModel.FindByDeviceID(l.ctx, req.DeviceID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrDeviceNotFound, "设备不存在")
		}
		l.Errorf("查询设备失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询设备失败")
	}

	// 3. requestId 幂等: 同一次上报重复投递只处理一次
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	} else {
		ok, derr := l.svcCtx.Redis.SetnxEx(eventIdemKey(requestID), "1", eventIdemTTL)
		if derr != nil {
			// Redis 异常不阻断主流程, 降级为不幂等
			l.Errorf("幂等写入失败, 跳过去重: %v", derr)
		} else if !ok {
			l.Infof("事件重复上报, 已忽略: requestId=%s", requestID)
			return &types.DeviceEventResp{RequestID: requestID, Accepted: false}, nil
		}
	}

	// 4. 组装消息
	occurredAt := req.OccurredAt
	if occurredAt <= 0 {
		occurredAt = time.Now().Unix()
	}
	msg := deviceEventMessage{
		RequestID:  requestID,
		DeviceID:   req.DeviceID,
		DeviceType: req.DeviceType,
		EventType:  req.EventType,
		OccurredAt: occurredAt,
		Payload:    json.RawMessage(rawPayload),
		Source:     "http-fallback",
	}
	value, err := json.Marshal(msg)
	if err != nil {
		l.Errorf("事件序列化失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "事件序列化失败")
	}

	// 5. 投递遥测 topic
	if err := l.svcCtx.Producer.Publish(l.ctx, kafka.TopicDeviceTelemetry, []byte(req.DeviceID), value); err != nil {
		l.Errorf("事件投递失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "事件投递失败")
	}

	// 6. 告警类事件额外投递告警 topic
	if _, ok := alarmEventTypes[req.EventType]; ok {
		if err := l.svcCtx.Producer.Publish(l.ctx, kafka.TopicAlarm, []byte(req.DeviceID), value); err != nil {
			// 告警投递失败不影响遥测结果, 仅记录
			l.Errorf("告警事件投递失败: requestId=%s, err=%v", requestID, err)
		}
	}

	l.Infof("事件上报成功: requestId=%s, deviceId=%s, eventType=%s", requestID, req.DeviceID, req.EventType)
	return &types.DeviceEventResp{RequestID: requestID, Accepted: true}, nil
}

const (
	eventIdemKeyPrefix = "m1:evt:"
	eventIdemTTL       = 600 // 10 分钟
)

func eventIdemKey(requestID string) string {
	return eventIdemKeyPrefix + requestID
}
