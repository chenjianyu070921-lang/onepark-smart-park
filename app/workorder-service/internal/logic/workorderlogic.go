package logic

import (
	"context"

	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type WorkorderLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewWorkorderLogic(ctx context.Context, svcCtx *svc.ServiceContext) *WorkorderLogic {
	return &WorkorderLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *WorkorderLogic) Workorder(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
