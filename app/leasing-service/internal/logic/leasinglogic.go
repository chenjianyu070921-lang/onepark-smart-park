package logic

import (
	"context"

	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type LeasingLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLeasingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LeasingLogic {
	return &LeasingLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *LeasingLogic) Leasing(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
