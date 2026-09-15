// Package server 提供 device-service 对外 gRPC 实现 (DeviceService).
// 供 M2 访客签入开门/停车道闸、M3 门禁远程开门、M5 大屏设备状态查询调用.
package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"onepark/app/device-service/internal/logic"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"
	devicepb "onepark/proto/device"

	"github.com/zeromicro/go-zero/core/logx"
)

// device 表 status 与 gRPC 状态字符串的映射
const (
	deviceStatusOffline = "offline"
	deviceStatusOnline  = "online"
	deviceStatusFault   = "fault"
)

type DeviceServer struct {
	svcCtx *svc.ServiceContext
	devicepb.UnimplementedDeviceServiceServer
}

func NewDeviceServer(svcCtx *svc.ServiceContext) *DeviceServer {
	return &DeviceServer{svcCtx: svcCtx}
}

// Ping 探活
func (s *DeviceServer) Ping(ctx context.Context, in *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// SendCommand 指令下发, 复用 HTTP 指令接口同一套落库逻辑, 返回 requestId.
// 当前仅落 command_log(status=0), MQTT 下行 Publish 为 P2.
func (s *DeviceServer) SendCommand(ctx context.Context, in *commonpb.DeviceCommand) (*commonpb.CommandResult, error) {
	if in.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id 不能为空")
	}
	if in.GetCommand() == "" {
		return nil, status.Error(codes.InvalidArgument, "command 不能为空")
	}

	payload := "{}"
	if len(in.GetPayload()) > 0 {
		payload = string(in.GetPayload())
	}

	resp, err := logic.NewDeviceCommandLogic(ctx, s.svcCtx).DeviceCommand(&types.DeviceCommandReq{
		DeviceID:    in.GetDeviceId(),
		CommandType: in.GetCommand(),
		Payload:     payload,
		Mode:        1,
	})
	if err != nil {
		logx.WithContext(ctx).Errorf("gRPC 指令下发失败: deviceId=%s, err=%v", in.GetDeviceId(), err)
		return nil, toGrpcError(err)
	}

	return &commonpb.CommandResult{
		DeviceId: in.GetDeviceId(),
		Success:  true,
		Message:  resp.RequestID,
	}, nil
}

// GetDevice 查询设备详情.
// TODO: type/latitude/longitude 依赖 device 表新增列, 当前返回零值, 待建表后补齐.
func (s *DeviceServer) GetDevice(ctx context.Context, in *devicepb.GetDeviceReq) (*devicepb.GetDeviceResp, error) {
	if in.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id 不能为空")
	}

	d, err := s.svcCtx.DeviceModel.FindByDeviceID(ctx, in.GetDeviceId())
	if err != nil {
		logx.WithContext(ctx).Errorf("gRPC 查询设备失败: deviceId=%s, err=%v", in.GetDeviceId(), err)
		return nil, toGrpcError(err)
	}

	resp := &devicepb.GetDeviceResp{
		DeviceId: d.DeviceID,
		Status:   mapDeviceStatus(d.Status),
	}
	if d.LastOnlineAt != nil {
		resp.LastSeen = d.LastOnlineAt.Unix()
	}
	return resp, nil
}

// toGrpcError 将业务 CodeError 映射为 gRPC 状态码, 便于调用方按 code 分支处理.
func toGrpcError(err error) error {
	if ce, ok := err.(*errorx.CodeError); ok {
		switch ce.Code {
		case errorx.ErrDeviceNotFound, errorx.ErrProductNotFound:
			return status.Error(codes.NotFound, ce.Msg)
		case errorx.ErrDeviceParamInvalid:
			return status.Error(codes.InvalidArgument, ce.Msg)
		case errorx.ErrDeviceOffline, errorx.ErrCommandSendFail:
			return status.Error(codes.FailedPrecondition, ce.Msg)
		}
	}
	return status.Error(codes.Internal, err.Error())
}

func mapDeviceStatus(s int8) string {
	switch s {
	case 1:
		return deviceStatusOnline
	case 2:
		return deviceStatusFault
	default:
		return deviceStatusOffline
	}
}
