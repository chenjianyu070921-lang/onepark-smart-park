package logic

import (
	"context"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ProductUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProductUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProductUpdateLogic {
	return &ProductUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ProductUpdateLogic) ProductUpdate(req *types.ProductUpdateReq) error {
	// todo: add your logic here and delete this line

	return nil
}
