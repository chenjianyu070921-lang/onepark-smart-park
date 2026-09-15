package logic

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

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
	// 先确认设备存在, 避免对已删除/不存在设备重复执行软删
	if _, err := l.svcCtx.DeviceModel.FindByDeviceID(l.ctx, req.DeviceID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrDeviceNotFound, "设备不存在")
		}
		l.Errorf("查询设备失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "查询设备失败")
	}

	if err := l.svcCtx.DeviceModel.SoftDelete(l.ctx, req.DeviceID); err != nil {
		l.Errorf("设备删除失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "设备删除失败")
	}

	// 同步清理设备影子
	if err := l.svcCtx.ShadowModel.Delete(l.ctx, req.DeviceID); err != nil {
		l.Errorf("影子删除失败: deviceId=%s, err=%v", req.DeviceID, err)
	}

	l.Infof("设备删除成功: deviceId=%s", req.DeviceID)
	return nil
}
