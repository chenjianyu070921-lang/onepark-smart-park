package logic

import (
	"context"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
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

func (l *DeviceCommandLogic) DeviceCommand(req *types.DeviceCommandReq) (resp *types.DeviceCommandResp, err error) {
	// todo: add your logic here and delete this line

	return
}
