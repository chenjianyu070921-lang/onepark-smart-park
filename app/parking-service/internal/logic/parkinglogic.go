package logic

import (
	"context"

	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ParkingLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewParkingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ParkingLogic {
	return &ParkingLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ParkingLogic) Parking(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
