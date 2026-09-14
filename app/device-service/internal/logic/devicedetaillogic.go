package logic

import (
	"context"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeviceDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceDetailLogic {
	return &DeviceDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeviceDetailLogic) DeviceDetail(req *types.DeviceDetailReq) (resp *types.DeviceDetailResp, err error) {
	// todo: add your logic here and delete this line

	return
}
