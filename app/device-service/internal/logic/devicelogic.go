package logic

import (
	"context"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeviceLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceLogic {
	return &DeviceLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeviceLogic) Device(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
