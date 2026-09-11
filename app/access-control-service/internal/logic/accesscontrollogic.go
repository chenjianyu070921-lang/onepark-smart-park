package logic

import (
	"context"

	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AccesscontrolLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAccesscontrolLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AccesscontrolLogic {
	return &AccesscontrolLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AccesscontrolLogic) Accesscontrol(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
