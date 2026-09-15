package logic

import (
	"context"
	"time"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeviceListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeviceListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeviceListLogic {
	return &DeviceListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeviceListLogic) DeviceList(req *types.DeviceListReq) (resp *types.DeviceListResp, err error) {
	page, size := normalizePage(req.Page, req.Size)

	list, total, err := l.svcCtx.DeviceModel.FindList(l.ctx, page, size, req.ProductKey, int8(req.Status))
	if err != nil {
		l.Errorf("查询设备列表失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询设备列表失败")
	}

	items := make([]types.DeviceListItem, 0, len(list))
	for _, d := range list {
		items = append(items, types.DeviceListItem{
			DeviceID:   d.DeviceID,
			DeviceName: d.DeviceName,
			ProductKey: d.ProductKey,
			Status:     d.Status,
			Location:   d.Location,
			CreatedAt:  d.CreatedAt.Format(time.DateTime),
		})
	}

	return &types.DeviceListResp{Total: int(total), List: items}, nil
}

// normalizePage 分页参数兜底, 单页上限 100.
func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
