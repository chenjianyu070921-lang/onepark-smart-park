package logic

import (
	"context"

	"onepark/app/energy-analysis-service/internal/svc"
	"onepark/app/energy-analysis-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type EnergyanalysisLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewEnergyanalysisLogic(ctx context.Context, svcCtx *svc.ServiceContext) *EnergyanalysisLogic {
	return &EnergyanalysisLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *EnergyanalysisLogic) Energyanalysis(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
