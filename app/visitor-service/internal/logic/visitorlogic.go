package logic

import (
	"context"

	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type VisitorLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVisitorLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VisitorLogic {
	return &VisitorLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *VisitorLogic) Visitor(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
