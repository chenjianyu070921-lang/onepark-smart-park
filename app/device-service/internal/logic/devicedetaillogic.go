package logic

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

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
	d, err := l.svcCtx.DeviceModel.FindByDeviceID(l.ctx, req.DeviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrDeviceNotFound, "设备不存在")
		}
		l.Errorf("查询设备失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询设备失败")
	}

	resp = &types.DeviceDetailResp{
		DeviceID:   d.DeviceID,
		DeviceName: d.DeviceName,
		ProductKey: d.ProductKey,
		ParkID:     d.ParkID,
		BuildingID: d.BuildingID,
		Floor:      d.Floor,
		Location:   d.Location,
		Status:     d.Status,
		CreatedAt:  d.CreatedAt.Format(time.DateTime),
	}
	if d.LastOnlineAt != nil {
		resp.LastOnlineAt = d.LastOnlineAt.Format(time.DateTime)
	}
	return resp, nil
}
