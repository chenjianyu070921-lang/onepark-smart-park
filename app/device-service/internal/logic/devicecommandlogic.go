package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/mqtt"

	"github.com/zeromicro/go-zero/core/logx"
)

// 指令状态常量统一定义在 model 包(model.CommandStatus*), 与 command_log.status 列绑定.
// DefaultCommandTimeout 兜底超时时间, 实际以配置 CommandTimeoutSec 为准.
const DefaultCommandTimeout = 30 * time.Second

// downlinkMessage 指令下行报文, 设备侧按此格式解析.
type downlinkMessage struct {
	RequestID   string          `json:"request_id"`
	CommandType string          `json:"command_type"`
	Payload     json.RawMessage `json:"payload"`
	SentAt      int64           `json:"sent_at"`
}

type DeviceCommandLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceCommandLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceCommandLogic {
	return &DeviceCommandLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// DeviceCommand 指令下发.
// 流程: 校验设备与参数 -> 落 command_log(status=0 待发送)
//
//	-> MQTT 下行 Publish(to onepark/cmd/{deviceId}/down) -> 状态推进为 1 已下发.
//
// 下行通道未就绪时(EMQX 未配置/断连)降级为仅落库, 由超时扫描任务兜底置为超时.
func (l *DeviceCommandLogic) DeviceCommand(req *types.DeviceCommandReq) (resp *types.DeviceCommandResp, err error) {
	// 1. 参数校验
	req.CommandType = strings.TrimSpace(req.CommandType)
	if req.DeviceID == "" {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "deviceId 不能为空")
	}
	if req.CommandType == "" {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "commandType 不能为空")
	}

	rawPayload := strings.TrimSpace(req.Payload)
	if rawPayload == "" {
		rawPayload = "{}"
	}
	if !json.Valid([]byte(rawPayload)) {
		return nil, errorx.NewError(errorx.ErrDeviceParamInvalid, "payload 必须是合法 JSON 字符串")
	}

	// goctl 的 default 标签对 POST body 不生效, 此处兜底
	if req.Mode == 0 {
		req.Mode = 1
	}

	// 2. 设备存在性校验
	device, err := l.svcCtx.DeviceModel.FindByDeviceID(l.ctx, req.DeviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrDeviceNotFound, "设备不存在")
		}
		l.Errorf("查询设备失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询设备失败")
	}

	// 3. 离线设备直接拒绝, 避免无效指令堆积
	// 注意: device.status 由遥测消费端(internal/mq)根据上下线报文实时回写,
	// 未接入上报的设备恒为离线, 属预期行为.
	if device.Status == model.DeviceStatusOffline {
		return nil, errorx.NewError(errorx.ErrDeviceOffline, "设备离线或未激活")
	}

	// 4. 落指令流水
	timeoutSec := l.svcCtx.Config.CommandTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = int(DefaultCommandTimeout.Seconds())
	}
	requestID := uuid.NewString()
	log := &model.CommandLog{
		RequestID:   requestID,
		DeviceID:    req.DeviceID,
		CommandType: req.CommandType,
		Payload:     datatypes.JSON(rawPayload),
		Mode:        req.Mode,
		Status:      model.CommandStatusPending,
		TimeoutAt:   time.Now().Add(time.Duration(timeoutSec) * time.Second),
	}
	if err := l.svcCtx.CommandLogModel.Insert(l.ctx, log); err != nil {
		l.Errorf("指令落库失败: %v", err)
		return nil, errorx.NewError(errorx.ErrCommandSendFail, "指令落库失败")
	}

	// 5. MQTT 下行下发: onepark/cmd/{deviceId}/down
	status := model.CommandStatusPending
	if l.svcCtx.Downlink != nil {
		value, mErr := json.Marshal(downlinkMessage{
			RequestID:   requestID,
			CommandType: req.CommandType,
			Payload:     json.RawMessage(rawPayload),
			SentAt:      time.Now().Unix(),
		})
		if mErr != nil {
			l.Errorf("指令报文序列化失败: requestId=%s, err=%v", requestID, mErr)
			value = []byte(rawPayload)
		}
		if err := l.svcCtx.Downlink.Publish(l.ctx, mqtt.CmdDownTopic(req.DeviceID), mqtt.QoSAtLeastOnce, value); err != nil {
			l.Errorf("指令下发失败: requestId=%s, deviceId=%s, err=%v", requestID, req.DeviceID, err)
			if uErr := l.svcCtx.CommandLogModel.UpdateStatus(l.ctx, requestID,
				model.CommandStatusFailed, []byte(errMsgJSON("下发失败: "+err.Error()))); uErr != nil {
				l.Errorf("指令失败状态回写异常: requestId=%s, err=%v", requestID, uErr)
			}
			return nil, errorx.NewError(errorx.ErrCommandSendFail, "指令下发失败")
		}
		status = model.CommandStatusSent
	} else {
		l.Errorf("下行通道未就绪, 指令仅落库: requestId=%s, deviceId=%s", requestID, req.DeviceID)
	}

	if status == model.CommandStatusSent {
		if err := l.svcCtx.CommandLogModel.MarkSent(l.ctx, requestID, status); err != nil {
			l.Errorf("指令下发状态回写失败: requestId=%s, err=%v", requestID, err)
		}
	}

	l.Infof("指令已受理: requestId=%s, deviceId=%s, commandType=%s, status=%d",
		requestID, req.DeviceID, req.CommandType, status)

	return &types.DeviceCommandResp{
		RequestID: requestID,
		Status:    status,
	}, nil
}

// errMsgJSON 构造 command_log.response 的错误信息 JSON.
func errMsgJSON(msg string) string {
	b, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		return `{"error":"unknown"}`
	}
	return string(b)
}
