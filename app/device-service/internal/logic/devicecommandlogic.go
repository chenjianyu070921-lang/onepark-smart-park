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

	"github.com/zeromicro/go-zero/core/logx"
)

// 指令状态: 与 command_log.status 列保持一致
const (
	CommandStatusPending  int8 = 0 // 待发送
	CommandStatusSent     int8 = 1 // 已下发
	CommandStatusSuccess  int8 = 2 // 执行成功
	CommandStatusFailed   int8 = 3 // 执行失败
	CommandStatusTimeout  int8 = 4 // 超时
	DefaultCommandTimeout      = 30 * time.Second
)

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
// 当前阶段: 校验设备与参数 -> 落 command_log(status=0 待发送) -> 返回 requestId.
// MQTT 下行 Publish(to onepark/cmd/{deviceId}/down) 为 P2, 届时在此处调用并把状态推进为 1.
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
	if device.Status == 0 {
		return nil, errorx.NewError(errorx.ErrDeviceOffline, "设备离线或未激活")
	}

	// 4. 落指令流水
	requestID := uuid.NewString()
	log := &model.CommandLog{
		RequestID:   requestID,
		DeviceID:    req.DeviceID,
		CommandType: req.CommandType,
		Payload:     datatypes.JSON(rawPayload),
		Mode:        req.Mode,
		Status:      CommandStatusPending,
		TimeoutAt:   time.Now().Add(DefaultCommandTimeout),
	}
	if err := l.svcCtx.CommandLogModel.Insert(l.ctx, log); err != nil {
		l.Errorf("指令落库失败: %v", err)
		return nil, errorx.NewError(errorx.ErrCommandSendFail, "指令落库失败")
	}

	l.Infof("指令已受理: requestId=%s, deviceId=%s, commandType=%s",
		requestID, req.DeviceID, req.CommandType)

	return &types.DeviceCommandResp{
		RequestID: requestID,
		Status:    CommandStatusPending,
	}, nil
}
