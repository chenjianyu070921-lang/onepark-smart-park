package logic

import (
	"context"

	"onepark/app/energy-data-service/internal/svc"
	"onepark/app/energy-data-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type EnergydataLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewEnergydataLogic(ctx context.Context, svcCtx *svc.ServiceContext) *EnergydataLogic {
	return &EnergydataLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *EnergydataLogic) Energydata(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
