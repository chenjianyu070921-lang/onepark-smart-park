package logic

import (
	"context"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

// ActiveParkingLogic 在场车辆查询逻辑(强制 status=1 停车中, 复用 ListParking 分页).
type ActiveParkingLogic struct {
	logx.Logger
	ctx               context.Context
	svcCtx            *svc.ServiceContext
	*ListParkingLogic // 复用 ListParkingLogic.query 分页能力
}

func NewActiveParkingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ActiveParkingLogic {
	return &ActiveParkingLogic{
		Logger:           logx.WithContext(ctx),
		ctx:              ctx,
		svcCtx:           svcCtx,
		ListParkingLogic: NewListParkingLogic(ctx, svcCtx),
	}
}

// ActiveParking 返回当前在场(停车中)车辆分页列表.
func (l *ActiveParkingLogic) ActiveParking(req *types.ActiveParkingReq) (resp *types.ParkingListResp, err error) {
	// 复用通用分页查询, 强制 status=停车中.
	return l.query(req.Page, req.PageSize, model.ParkingStatusParking, "")
}
