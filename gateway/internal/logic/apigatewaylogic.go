package logic

import (
	"context"

	"onepark/gateway/internal/svc"
	"onepark/gateway/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ApigatewayLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewApigatewayLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ApigatewayLogic {
	return &ApigatewayLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ApigatewayLogic) Apigateway(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
