package logic

import (
	"context"

	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DashboardLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDashboardLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DashboardLogic {
	return &DashboardLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DashboardLogic) Dashboard(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
