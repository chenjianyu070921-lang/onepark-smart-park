package logic

import (
	"context"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeviceDeleteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceDeleteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceDeleteLogic {
	return &DeviceDeleteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeviceDeleteLogic) DeviceDelete(req *types.DeviceDeleteReq) error {
	// todo: add your logic here and delete this line

	return nil
}
